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

package held

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp checks that a start aimed at another app is not held: the
// scheduler refuses a reminder on another app's workflow actor, so the start
// keeps today's behaviour and blocks until the caller's deadline.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithHeldOperations(),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	_, err := c.workflow.ManagementClientN(t, ctx, 0).ScheduleNewWorkflow(sctx, "wf",
		api.WithInstanceID("cross-app"),
		api.WithAppID(c.workflow.DaprN(1).AppID()),
	)
	require.Error(t, err)

	assert.Empty(t, heldKeys(t, ctx, c.workflow, "cross-app"))
}
