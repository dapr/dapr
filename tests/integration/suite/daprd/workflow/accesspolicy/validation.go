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

package accesspolicy

import (
	"context"
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

// validation tests that the standalone CRD-based validator rejects invalid
// WorkflowAccessPolicy YAML files during disk loading and hot-reload.
// Invalid policies are skipped (not loaded), leaving the system in an
// allow-all state. A valid policy written after invalid ones IS loaded.
type validation struct {
	daprd         *daprd.Daprd
	place         *placement.Placement
	sched         *scheduler.Scheduler
	resDir        string
	validationLog *logline.LogLine
}

func (v *validation) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t)
	v.sched = scheduler.New(t)
	db := sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	v.resDir = t.TempDir()

	v.validationLog = logline.New(t,
		logline.WithStdoutLineContains(
			`\"empty-appid\" failed validation`,
			`\"empty-ops\" failed validation`,
			`\"empty-rule\" failed validation`,
		),
	)

	v.daprd = daprd.New(t,
		daprd.WithAppID("wfacl-validation"),
		daprd.WithResourcesDir(v.resDir),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(v.place.Address()),
		daprd.WithScheduler(v.sched),
		daprd.WithExecOptions(exec.WithStdout(v.validationLog.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(v.validationLog, db, v.place, v.sched, v.daprd),
	}
}

func (v *validation) Run(t *testing.T, ctx context.Context) {
	v.place.WaitUntilRunning(t, ctx)
	v.sched.WaitUntilRunning(t, ctx)
	v.daprd.WaitUntilRunning(t, ctx)

	t.Run("empty appID is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy-appid.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: empty-appid
spec:
  rules:
  - callers:
    - appID: ""
    workflows:
    - name: "WF"
      operations: [schedule]
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"empty-appid\" failed validation`, time.Second*20, time.Millisecond*10)
	})

	t.Run("empty operations array is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy-ops.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: empty-ops
spec:
  rules:
  - callers:
    - appID: "some-app"
    workflows:
    - name: "WF"
      operations: []
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"empty-ops\" failed validation`, time.Second*20, time.Millisecond*10)
	})

	t.Run("rule with neither workflows nor activities is rejected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "policy-rule.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: empty-rule
spec:
  rules:
  - callers:
    - appID: "some-app"
`), 0o600))

		v.validationLog.EventuallyContains(t, `\"empty-rule\" failed validation`, time.Second*20, time.Millisecond*10)
	})

	t.Run("valid policy is loaded after invalid ones are skipped", func(t *testing.T) {
		for _, f := range []string{"policy-appid.yaml", "policy-ops.yaml", "policy-rule.yaml"} {
			os.Remove(filepath.Join(v.resDir, f))
		}
		require.NoError(t, os.WriteFile(filepath.Join(v.resDir, "valid-policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: valid-allow
spec:
  rules:
  - callers:
    - appID: "other-app"
    workflows:
    - name: "*"
      operations: [schedule]
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			policies := v.daprd.GetMetadata(t, ctx).WorkflowAccessPolicies
			if !assert.Len(c, policies, 1) {
				return
			}
			assert.Equal(c, "valid-allow", policies[0].GetName())
		}, time.Second*20, time.Millisecond*10)
	})
}
