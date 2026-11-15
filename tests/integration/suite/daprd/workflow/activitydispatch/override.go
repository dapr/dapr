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

package activitydispatch

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(override))
}

// override asserts that a per-name dispatchMode of pull applies to that
// activity only when the app dispatches hashed.
type override struct {
	workflow *workflow.Workflow
}

func (s *override) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pulloverride
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
    activityConcurrencyLimits:
      - name: pullact
        dispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *override) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	assertReminderFlags(t, ctx, s.workflow,
		call{name: "pullact", pull: true},
		call{name: "hashedact", pull: false},
	)
}
