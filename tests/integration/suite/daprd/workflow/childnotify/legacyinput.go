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

package childnotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(legacyinput))
}

// legacyinput has a signed cross-app child continue as new, then rewrites
// its creation-input row into the bare StringValue form the first binary
// that kept the input wrote, and restarts the child's sidecar so the row is
// loaded cold. The child must load, attest its completion with that input
// and resolve the parent's task; the row is rewritten in the stamped form.
type legacyinput struct {
	workflow *workflow.Workflow
}

func (l *legacyinput) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t, workflow.WithDaprds(2), workflow.WithMTLS(t))
	return []framework.Option{framework.WithProcesses(l.workflow)}
}

func (l *legacyinput) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const childID = "legacyinput-child"
	childApp := l.workflow.DaprN(1).AppID()
	require.NoError(t, l.workflow.RegistryN(1).AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if input != "second" {
			ctx.ContinueAsNew("second")
			return nil, nil
		}
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return input, nil
	}))
	require.NoError(t, l.workflow.Registry().AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput("first"),
			task.WithChildWorkflowInstanceID(childID),
			task.WithChildWorkflowAppID(childApp),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	cl := l.workflow.BackendClient(t, ctx)
	child := l.workflow.BackendClientN(t, ctx, 1)

	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := child.FetchWorkflowMetadata(ctx, childID, api.WithFetchPayloads(true))
		if assert.NoError(c, merr) {
			assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
			assert.JSONEq(c, `"second"`, meta.GetInput().GetValue())
		}
	}, time.Second*20, time.Millisecond*10)

	// Rewrite the kept input as the first binary wrote it: a bare
	// StringValue without the creating parent's stamp.
	db := l.workflow.DB()
	histKey, _ := db.FirstStateValue(t, ctx, childID, "history")
	rowKey := histKey[:strings.LastIndex(histKey, "||")+2] + "creation-input"
	var stamped string
	require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT value FROM "+db.TableName()+" WHERE key = ?", rowKey).Scan(&stamped))
	legacy, err := proto.Marshal(wrapperspb.String(`"first"`))
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, rowKey, legacy)

	l.workflow.DaprN(1).RestartGraceful(t, ctx)
	l.workflow.WaitUntilRunning(t, ctx)
	child = l.workflow.BackendClientN(t, ctx, 1)

	require.NoError(t, child.RaiseEvent(ctx, childID, "go"))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "the parent must accept the completion attested with the legacy input")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"second"`, meta.GetOutput().GetValue())

	var rewritten string
	require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT value FROM "+db.TableName()+" WHERE key = ?", rowKey).Scan(&rewritten))
	assert.Equal(t, stamped, rewritten, "the row is rewritten in the stamped form")
	_ = wf.WaitForRuntimeStatus
}
