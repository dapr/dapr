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
	suite.Register(new(flag))
}

// flag asserts that the reminder of an activity carries the pull flag when the
// app dispatches by pull, and not for a name overridden to hashed.
type flag struct {
	workflow *workflow.Workflow
}

func (s *flag) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullflag
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
    activityDispatchMode: pull
    activityConcurrencyLimits:
      - name: hashedact
        maxConcurrent: 5
        dispatchMode: hashed
`)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *flag) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	assertReminderFlags(t, ctx, s.workflow,
		call{name: "pullact", pull: true},
		call{name: "hashedact", pull: false},
	)
}
