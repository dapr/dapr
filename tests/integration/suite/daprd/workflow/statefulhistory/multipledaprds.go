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

package statefulhistory

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multipledaprds))
}

// multipledaprds runs many workflows across two daprd sidecars that form a single
// clustered app (shared app ID + WorkflowsClusteredDeployment), each with its own
// connected worker, sharing one placement and state store. Workflow actors are
// distributed across both sidecars, so each worker independently warms and serves
// deltas for the instances placed on its sidecar. All workflows must complete
// correctly, both workers must do work, and deltas must flow.
type multipledaprds struct {
	workflow *workflow.Workflow
}

func (m *multipledaprds) Setup(t *testing.T) []framework.Option {
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
	appID := uuid.New().String()
	m.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithConfigManifests(t, config)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithConfigManifests(t, config)),
	)
	return []framework.Option{framework.WithProcesses(m.workflow)}
}

func (m *multipledaprds) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const (
		activityCount = 4
		workflowCount = 20
	)

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("Accumulate", accumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	worker0 := m.workflow.ConnectWorkerN(t, ctx, 0, newRegistry())
	worker1 := m.workflow.ConnectWorkerN(t, ctx, 1, newRegistry())
	m.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	m.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)

	ids := make([]api.InstanceID, workflowCount)
	for i := range ids {
		id, err := worker0.Client.ScheduleNewWorkflow(ctx, "Accumulate")
		require.NoError(t, err)
		ids[i] = id
	}

	// Wait for completion in the background so that, if an instance stalls (a rare
	// intermittent hang seen only under heavy CI contention), we can capture goroutine
	// stacks from the workers (this process) and both daprds before the test's deadline
	// force-kills the sidecars. TODO: remove the diagnostics once the stall is root-caused.
	waitDone := make(chan error, 1)
	go func() {
		for _, id := range ids {
			meta, werr := worker0.Client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
			if werr != nil {
				waitDone <- fmt.Errorf("waiting for %s: %w", id, werr)
				return
			}
			if !api.WorkflowMetadataIsComplete(meta) {
				waitDone <- fmt.Errorf("%s did not complete: status=%s", id, meta.GetRuntimeStatus())
				return
			}
			if got := meta.GetOutput().GetValue(); got != strconv.Itoa(activityCount) {
				waitDone <- fmt.Errorf("%s wrong output: got %q want %d", id, got, activityCount)
				return
			}
		}
		waitDone <- nil
	}()

	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		m.dumpStallDiagnostics(t, ctx)
		t.Fatal("workflows did not all complete within 30s; goroutine diagnostics dumped above")
	}

	work0 := worker0.Observer.FullSends() + worker0.Observer.Deltas()
	work1 := worker1.Observer.FullSends() + worker1.Observer.Deltas()
	totalDeltas := worker0.Observer.Deltas() + worker1.Observer.Deltas()

	assert.Positive(t, work0, "worker on daprd 0 must execute some instances")
	assert.Positive(t, work1, "worker on daprd 1 must execute some instances")
	assert.Positive(t, totalDeltas, "deltas must flow with workflows spread across both sidecars")
}

// dumpStallDiagnostics logs goroutine stacks from this test process (which hosts the
// backend workers) and from every daprd's pprof endpoint, so an intermittent completion
// stall in CI produces the stuck stacks needed to root-cause it.
// TODO: remove once the stall is understood.
func (m *multipledaprds) dumpStallDiagnostics(t *testing.T, ctx context.Context) {
	t.Helper()

	buf := make([]byte, 8<<20)
	n := runtime.Stack(buf, true)
	t.Logf("STALL DIAGNOSTIC: test-process (worker) goroutines:\n%s", buf[:n])

	for i := 0; i < 2; i++ {
		addr := fmt.Sprintf("127.0.0.1:%d", m.workflow.DaprN(i).ProfilePort())
		reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
			fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=2", addr), nil)
		if err != nil {
			cancel()
			t.Logf("STALL DIAGNOSTIC: daprd[%d] request build failed: %v", i, err)
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Logf("STALL DIAGNOSTIC: daprd[%d] pprof fetch failed: %v", i, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		t.Logf("STALL DIAGNOSTIC: daprd[%d] (%s) goroutines:\n%s", i, addr, body)
	}
}
