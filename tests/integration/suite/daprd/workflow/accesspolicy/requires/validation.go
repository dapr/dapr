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

package requires

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(validation))
}

// CEL rules on RequiredEvent: eventType=event is for status=Raised,
// eventType=activity|workflow forbids status=Raised.
type validation struct {
	daprd  *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
	resDir string
	logs   *logline.LogLine
}

func (v *validation) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t)
	v.sched = scheduler.New(t)
	db := sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	v.resDir = t.TempDir()

	v.logs = logline.New(t,
		logline.WithStdoutLineContains(
			`\"event-with-started\" failed validation`,
			`\"event-with-completed\" failed validation`,
			`\"activity-with-raised\" failed validation`,
			`\"workflow-with-raised\" failed validation`,
			`\"requires-on-terminate\" failed validation`,
		),
	)

	v.daprd = daprd.New(t,
		daprd.WithAppID("wfacl-requires-validation"),
		daprd.WithResourcesDir(v.resDir),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(v.place.Address()),
		daprd.WithScheduler(v.sched),
		daprd.WithExecOptions(exec.WithStdout(v.logs.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(v.logs, db, v.place, v.sched, v.daprd),
	}
}

func (v *validation) Run(t *testing.T, ctx context.Context) {
	v.place.WaitUntilRunning(t, ctx)
	v.sched.WaitUntilRunning(t, ctx)
	v.daprd.WaitUntilRunning(t, ctx)

	policyYAML := func(name string, requires string) []byte {
		return fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: %s
spec:
  rules:
  - callers:
    - appID: caller
    activities:
    - name: GatedActivity
      requires:
%s
`, name, requires)
	}

	assertNoneLoaded := func(t *testing.T) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, v.daprd.GetMetadata(t, ctx).WorkflowAccessPolicies)
		}, time.Second*20, time.Millisecond*10)
	}

	t.Run("invalid eventType activity.raised is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "invalid-eventtype-activity-raised.yaml"),
			policyYAML("invalid-eventtype-activity-raised",
				`      - eventType: activity.raised
        name: X
        appID: caller`), 0o600))
		v.logs.EventuallyContains(t, `\"invalid-eventtype-activity-raised\" failed validation`, time.Second*20, time.Millisecond*10)
		assertNoneLoaded(t)
	})

	t.Run("invalid eventType workflow.raised is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "invalid-eventtype-workflow-raised.yaml"),
			policyYAML("invalid-eventtype-workflow-raised",
				`      - eventType: workflow.raised
        name: X
        appID: caller`), 0o600))
		v.logs.EventuallyContains(t, `\"invalid-eventtype-workflow-raised\" failed validation`, time.Second*20, time.Millisecond*10)
		assertNoneLoaded(t)
	})

	t.Run("invalid eventType event.started is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "invalid-eventtype-event-started.yaml"),
			policyYAML("invalid-eventtype-event-started",
				`      - eventType: event.started
        name: X
        appID: caller`), 0o600))
		v.logs.EventuallyContains(t, `\"invalid-eventtype-event-started\" failed validation`, time.Second*20, time.Millisecond*10)
		assertNoneLoaded(t)
	})

	t.Run("requires on a rule with a non-schedule operation is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "requires-on-terminate.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requires-on-terminate
spec:
  rules:
  - callers:
    - appID: caller
    workflows:
    - name: TargetWF
      operations:
      - schedule
      - terminate
      requires:
      - eventType: activity.completed
        name: X
        appID: caller
`), 0o600))
		v.logs.EventuallyContains(t, `\"requires-on-terminate\" failed validation`, time.Second*20, time.Millisecond*10)
		assertNoneLoaded(t)
	})

	t.Run("valid combinations load successfully", func(t *testing.T) {
		for _, f := range []string{"invalid-eventtype-activity-raised.yaml", "invalid-eventtype-workflow-raised.yaml", "invalid-eventtype-event-started.yaml", "requires-on-terminate.yaml"} {
			os.Remove(filepath.Join(v.resDir, f))
		}
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "valid.yaml"),
			policyYAML("valid-requires",
				`      - eventType: activity.completed
        name: X
        appID: caller
      - eventType: workflow.started
        name: Y
        appID: caller
      - eventType: event.raised
        name: Z
        appID: caller`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			policies := v.daprd.GetMetadata(t, ctx).WorkflowAccessPolicies
			if !assert.Len(c, policies, 1) {
				return
			}
			assert.Equal(c, "valid-requires", policies[0].GetName())
		}, time.Second*20, time.Millisecond*10)
	})
}
