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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fastpath))
}

// fastpath runs the basic scenario with WorkflowsFastPath, whose local
// drives complete workflows without a scheduler round trip.
type fastpath struct {
	workflow *workflow.Workflow
}

func (f *fastpath) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, policy(t, basicPolicy)),
	)
	return []framework.Option{framework.WithProcesses(f.workflow)}
}

func (f *fastpath) Run(t *testing.T, ctx context.Context) {
	runBasic(t, ctx, f.workflow)
}
