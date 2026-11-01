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

package fallback

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(disabled))
}

// disabled checks that, without the feature, a start with no worker
// connected still blocks until the caller's deadline: no daprd hosts the
// workflow actor type.
type disabled struct {
	workflow *workflow.Workflow
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	_, err := d.workflow.ManagementClient(t, ctx).ScheduleNewWorkflow(sctx, "wf", api.WithInstanceID("no-fallback"))
	require.Error(t, err)
}
