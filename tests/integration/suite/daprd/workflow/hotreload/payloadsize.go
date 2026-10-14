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

package hotreload

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(payloadSize))
}

// payloadSize ensures that the runtime restarted by SIGHUP records the
// workflow and activity payload size ratios on its own meter. The replacement
// registers its views on a new meter, which must get its own payload ratio
// distribution rather than the one the previous meter registered.
type payloadSize struct {
	workflow *workflow.Workflow
	log      *log.Log
}

// 64 KiB of an 8 MiB max body size.
const (
	payloadInputSize = 64 << 10
	payloadMaxBody   = 8 << 20
)

func (p *payloadSize) Setup(t *testing.T) []framework.Option {
	input := strings.Repeat("x", payloadInputSize)

	p.log = log.New()
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("8Mi"),
			daprd.WithExecOptions(exec.WithStdout(p.log), exec.WithStderr(p.log)),
		),
		workflow.WithAddActivityN(t, 0, "echo", func(ctx task.ActivityContext) (any, error) {
			var in string
			if err := ctx.GetInput(&in); err != nil {
				return nil, err
			}
			return in, nil
		}),
		workflow.WithAddWorkflowN(t, 0, "payloadsize", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("echo", task.WithActivityInput(input)).Await(nil)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *payloadSize) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	appID := p.workflow.Dapr().AppID()
	wfLabels := "|app_id:" + appID + "|namespace:|workflow_name:payloadsize"
	actLabels := "|activity_name:echo" + wfLabels

	run := func() {
		client := p.workflow.BackendClient(t, ctx)
		id, err := client.ScheduleNewWorkflow(ctx, "payloadsize")
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(meta))
	}

	// One run dispatches the workflow twice (ExecutionStarted, then
	// TaskCompleted) and the activity once.
	assertRuns := func(runs int) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			m := p.workflow.Dapr().Metrics(c, ctx).All()
			assert.Equal(c, 2*runs, int(m["dapr_runtime_workflow_payload_size_ratio_count"+wfLabels]))
			assert.Equal(c, runs, int(m["dapr_runtime_workflow_activity_payload_size_ratio_count"+actLabels]))
			assert.GreaterOrEqual(c, m["dapr_runtime_workflow_activity_payload_size_ratio_sum"+actLabels], float64(runs*payloadInputSize)/payloadMaxBody)
		}, 10*time.Second, 50*time.Millisecond)
	}

	run()
	assertRuns(1)

	p.log.Reset()
	p.workflow.Dapr().SignalHUP(t)

	// SIGHUP is handled asynchronously, so readiness can pass on the old runtime.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, p.log.Contains("dapr initialized. Status: Running"))
	}, 20*time.Second, 10*time.Millisecond)
	p.workflow.WaitUntilRunning(t, ctx)

	// The replacement runtime starts from an empty meter, so two runs on it
	// report two runs' worth of samples, not three.
	run()
	run()
	assertRuns(2)
}
