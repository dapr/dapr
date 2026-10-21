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

package fallback

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
)

// newPair returns two daprds of one app with staged operations, the second
// with the given fallback damping period in place of the default.
func newPair(t *testing.T, damping time.Duration) *workflow.Workflow {
	t.Helper()

	appID := uuid.NewString()
	return workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackDamping(t, damping)),
	)
}

// hostsWorkflows returns whether the daprd at index hosts the workflow actor
// type, as a fallback host or with a worker. A failed metadata read fails c
// and returns false.
func hostsWorkflows(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) bool {
	_, hosted := wf.DaprN(index).ActiveActorCount(c, ctx, wf.WorkflowActorType(index))
	return hosted
}

// waitCompleted waits for the workflow to complete and returns its metadata.
func waitCompleted(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, id api.InstanceID) *backend.WorkflowMetadata {
	t.Helper()

	meta, err := c.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, id)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
	return meta
}

// assertCompleted waits for every workflow to complete.
func assertCompleted(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, ids ...api.InstanceID) {
	t.Helper()

	for _, id := range ids {
		waitCompleted(t, ctx, c, id)
	}
}

// startConcurrently runs body in a loop on each of n goroutines, with the
// goroutine and the iteration numbers, until the returned stop is called.
// When body returns true, the instance ID it returns is recorded; stop waits
// for the goroutines and returns the recorded IDs.
func startConcurrently(n int, body func(g, i int) (api.InstanceID, bool)) (stop func() []api.InstanceID) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	var lock sync.Mutex
	var ids []api.InstanceID
	for g := range n {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}

				if id, ok := body(g, i); ok {
					lock.Lock()
					ids = append(ids, id)
					lock.Unlock()
				}
			}
		})
	}

	return func() []api.InstanceID {
		close(done)
		wg.Wait()
		return ids
	}
}

// readErrors records whether a read failed, for a background poll which
// must not fail the test on a read cut short by its own end.
type readErrors struct{ failed bool }

func (r *readErrors) Errorf(string, ...any) { r.failed = true }
