/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chaos

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"
	wfclient "github.com/dapr/durabletask-go/workflow"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remotestream))
}

type remotestream struct {
	workflow *workflow.Workflow
}

func (r *remotestream) Setup(t *testing.T) []framework.Option {
	// Share a single appID across both daprds and enable
	// WorkflowsClusteredDeployment so they form a single cluster of workflow
	// actor hosts. Without this each daprd would default to a random UUID
	// appID, host its own actor type, and never route a workflow call
	// across the cluster - so the cross-daprd CallActorStream path the test
	// is asserting on would not be exercised at all.
	uid, err := uuid.NewRandom()
	require.NoError(t, err)
	appID := uid.String()

	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`

	wopts := make([]workflow.Option, 0, 3)
	wopts = append(wopts, workflow.WithDaprds(2))
	for i := range 2 {
		wopts = append(wopts, workflow.WithDaprdOptions(i,
			daprd.WithAppID(appID),
			daprd.WithConfigManifests(t, config),
		))
	}

	r.workflow = workflow.New(t, wopts...)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *remotestream) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var release atomic.Bool
	var activitiesStarted atomic.Int32
	activityFn := func(actx task.ActivityContext) (any, error) {
		activitiesStarted.Add(1)
		for !release.Load() {
			select {
			case <-actx.Context().Done():
				return nil, actx.Context().Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
		return "done", nil
	}
	workflowFn := func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}

	for i := range 2 {
		reg := r.workflow.RegistryN(i)
		require.NoError(t, reg.AddActivityN("act", activityFn))
		require.NoError(t, reg.AddWorkflowN("wf", workflowFn))
	}

	for i := range 2 {
		r.workflow.BackendClientN(t, ctx, i)
	}

	// Wait client is bound to daprd-0 only. Workflows hashed to daprd-1
	// will therefore exercise the cross-daprd CallActorStream proxy path
	// in handleStream.
	wfc := r.workflow.WorkflowClientN(t, ctx, 0)
	gclient := r.workflow.GRPCClient(t, ctx)

	const (
		remotestreamWorkflows = 20
	)

	ids := make([]string, remotestreamWorkflows)
	for i := range remotestreamWorkflows {
		ids[i] = fmt.Sprintf("remotestream-wf-%d", i)
		_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "wf",
			InstanceId:        ids[i],
		})
		require.NoError(t, err)
	}

	// Wait for every workflow to have its activity in flight; this means
	// the workflow is firmly in the RUNNING state and that subsequent
	// WaitForWorkflowCompletion calls will register a streamFns subscriber
	// (rather than short-circuit on already-terminal state).
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.GreaterOrEqual(co, int(activitiesStarted.Load()), remotestreamWorkflows)
	}, 30*time.Second, 50*time.Millisecond)

	type result struct {
		id     string
		status string
		err    error
	}
	results := make(chan result, remotestreamWorkflows)

	// Fan out the streaming waits first so every workflow has a live
	// subscriber when it completes; only then release the activities. Each
	// goroutine first issues a WaitForWorkflowStart, which uses the same
	// handleStream path as WaitForWorkflowCompletion but returns immediately
	// on a RUNNING workflow (the short-circuit at the top of handleStream).
	// The corresponding actor lock has therefore been taken and released by
	// the goroutine before the warmup channel is signalled, so the goroutine's
	// subsequent WaitForWorkflowCompletion gRPC is the next caller in line
	// for that actor's lock; the main goroutine collects N warmup signals,
	// drains them via a barrier, and only then releases the activities. There
	// is no fixed sleep: the wait time is dictated by the actor system itself.
	warmup := make(chan struct{}, remotestreamWorkflows)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string, wfc *wfclient.Client) {
			defer wg.Done()
			_, werr := wfc.WaitForWorkflowStart(ctx, id)
			if werr != nil {
				results <- result{id: id, err: fmt.Errorf("warmup WaitForWorkflowStart failed: %w", werr)}
				warmup <- struct{}{}
				return
			}
			warmup <- struct{}{}
			meta, err := wfc.WaitForWorkflowCompletion(ctx, id)
			var status string
			if meta != nil {
				status = meta.String()
			}
			results <- result{id: id, status: status, err: err}
		}(id, wfc)
	}

	for range ids {
		select {
		case <-warmup:
		case <-ctx.Done():
			require.Fail(t, "WaitForWorkflowCompletion goroutines did not all warm up before timeout")
		}
	}

	// Final synchronisation: each workflow actor's lock has now been used by the
	// goroutine's WaitForWorkflowStart and released. The follow-up
	// WaitForWorkflowCompletion gRPC is already in flight. Issue a synchronous
	// GetWorkflowBeta1 per workflow; that call takes the actor's lock, which is
	// FIFO ordered with the handleStream call queued by
	// WaitForWorkflowCompletion. The GetWorkflowBeta1 returning RUNNING implies
	// handleStream's unlock() (and therefore streamFns registration) has
	// completed for every workflow.
	for _, id := range ids {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        id,
			WorkflowComponent: "dapr",
		})
		require.NoError(t, gerr)
		require.Equal(t, "RUNNING", resp.GetRuntimeStatus())
	}

	release.Store(true)

	wg.Wait()
	close(results)

	var stuck []string
	var completed atomic.Int32
	for res := range results {
		if res.err != nil {
			stuck = append(stuck, fmt.Sprintf("%s err=%v", res.id, res.err))
			continue
		}
		if res.status != "COMPLETED" {
			stuck = append(stuck, fmt.Sprintf("%s status=%s", res.id, res.status))
			continue
		}
		completed.Add(1)
	}

	require.Empty(t, stuck,
		"workflows that did not return COMPLETED via WaitForWorkflowCompletion through daprd-0 (some of these are owned by daprd-1 and exercise the remote-stream path): %v",
		stuck)
	assert.Equal(t, int32(remotestreamWorkflows), completed.Load(),
		"every workflow must complete via the streaming wait")
}
