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
	suite.Register(new(clustered))
}

// clustered runs the basic scenario with WorkflowsClusteredDeployment, which
// changes how the workflow and retentioner actor types are hosted.
type clustered struct {
	workflow *workflow.Workflow
}

func (c *clustered) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithClusteredDeployment(true),
		workflow.WithDaprdOptions(0, policy(t, basicPolicy)),
	)
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *clustered) Run(t *testing.T, ctx context.Context) {
	runBasic(t, ctx, c.workflow)
}
