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

package niceness

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hostlimit))
}

// hostlimit verifies that a sidecar's own limit is advertised to the
// scheduler, which then holds the waiting workflows itself and orders them
// by niceness, with no global limit configured.
type hostlimit struct {
	workflow *workflow.Workflow
}

const hostlimitAppID = "niceness-hostlimit"

func (h *hostlimit) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: nicenesshostlimit
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    niceness:
      default: 10
      workflows:
      - name: hi
        niceness: -10
`
	h.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(hostlimitAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *hostlimit) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)
	runOrder(t, ctx, h.workflow, hostlimitAppID)
}
