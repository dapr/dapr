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
	"fmt"
	"testing"

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
	suite.Register(new(nopolicy))
}

// nopolicy tests that when no WorkflowAccessPolicy resources exist (but the
// feature flag is enabled), all cross-app workflow calls succeed (backward
// compatible behavior). No mTLS needed because no policies means nil
// CompiledPolicies which short-circuits before identity extraction.
type nopolicy struct {
	wf *workflow.Workflow
}

func (n *nopolicy) Setup(t *testing.T) []framework.Option {
	n.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("nopol-caller"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithAppID("nopol-target"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.wf),
	}
}

func (n *nopolicy) Run(t *testing.T, ctx context.Context) {
	n.wf.WaitUntilRunning(t, ctx)

	n.wf.Registry().AddWorkflowN("AnyWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("AnyActivity",
			task.WithActivityAppID(n.wf.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("activity failed: %w", err)
		}
		return output, nil
	})

	n.wf.RegistryN(1).AddActivityN("AnyActivity", func(ctx task.ActivityContext) (any, error) {
		return "no-policy-ok", nil
	})

	client0 := n.wf.BackendClient(t, ctx)
	n.wf.BackendClientN(t, ctx, 1)

	t.Run("no policies means allow all", func(t *testing.T) {
		id, err := client0.ScheduleNewWorkflow(ctx, "AnyWorkflow")
		require.NoError(t, err)

		metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Equal(t, `"no-policy-ok"`, metadata.GetOutput().GetValue())
	})
}

func configWithFeatureFlag() string {
	return `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`
}
