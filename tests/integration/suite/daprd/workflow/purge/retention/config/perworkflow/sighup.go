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
package perworkflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(sighup))
}

const (
	sighupBefore = `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfsighup
spec:
  workflow:
    stateRetentionPolicy:
      anyTerminal: "1h"
`
	sighupAfter = sighupBefore + `      workflows:
        - name: fast
          completed: "1s"
`
)

// sighup: a SIGHUP reload picks up new entries for workflows that complete
// afterwards and does not touch instances that are already terminal.
type sighup struct {
	workflow   *workflow.Workflow
	configFile string
	log        *log.Log
}

func (s *sighup) Setup(t *testing.T) []framework.Option {
	s.configFile = os.WriteFileYaml(t, sighupBefore)
	s.log = log.New()
	s.workflow = workflow.New(t, workflow.WithDaprdOptions(0,
		daprd.WithConfigs(s.configFile),
		daprd.WithExecOptions(exec.WithStdout(s.log), exec.WithStderr(s.log)),
	))
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *sighup) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, s.workflow.Registry().AddWorkflowN("fast", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := s.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, s.workflow)

	before, err := cl.ScheduleNewWorkflow(ctx, "fast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, before)
	require.NoError(t, err)

	os.WriteFileTo(t, s.configFile, sighupAfter)
	s.log.Reset()
	s.workflow.Dapr().SignalHUP(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, s.log.Contains("dapr initialized. Status: Running"))
	}, time.Second*20, time.Millisecond*10)
	s.workflow.WaitUntilRunning(t, ctx)
	cl = s.workflow.BackendClient(t, ctx)

	after, err := cl.ScheduleNewWorkflow(ctx, "fast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, after)
	require.NoError(t, err)
	waitPurged(t, rows, string(after), "the reloaded entry did not purge")
	assert.Positive(t, rows(t, string(before)), "a reloaded policy must not apply retroactively")
}
