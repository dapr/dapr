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

package chaos

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(eventdedup))
}

// eventdedup verifies that a *redelivered* external event (the same already-
// persisted EventRaised re-sent to the workflow actor, e.g. an AddWorkflowEvent
// invocation retried under placement churn) is not applied twice, while two
// genuinely distinct RaiseEvents of the same name are both delivered.
type eventdedup struct {
	workflow *workflow.Workflow
}

func (e *eventdedup) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *eventdedup) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "eventdedup-wf"

	r := e.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var a, b string
		if err := octx.WaitForSingleEvent("go", -1).Await(&a); err != nil {
			return nil, err
		}
		if err := octx.WaitForSingleEvent("go", -1).Await(&b); err != nil {
			return nil, err
		}
		return a + b, nil
	}))

	bc := e.workflow.BackendClient(t, ctx)
	gclient := e.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// First (real) event satisfies the first wait.
	require.NoError(t, bc.RaiseEvent(ctx, api.InstanceID(wfID), "go", api.WithEventPayload("v1")))

	// Wait until the first "go" is consumed into history (so the workflow has
	// advanced to the second wait), then capture the exact persisted event.
	var raised *protos.HistoryEvent
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		hist, herr := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
		require.NoError(co, herr)
		raised = nil
		for _, he := range hist.GetEvents() {
			if er := he.GetEventRaised(); er != nil && er.GetName() == "go" {
				raised = he
			}
		}
		require.NotNil(co, raised, "first EventRaised('go') not yet in history")
	}, 15*time.Second, 10*time.Millisecond)

	dupBytes, err := proto.Marshal(raised)
	require.NoError(t, err)

	actorType := "dapr.internal." + e.workflow.Dapr().Namespace() + "." + e.workflow.Dapr().AppID() + ".workflow"

	// Redeliver the byte-identical event. With the dedup it is dropped (and the
	// wake-up reminder re-asserted); the second wait stays unsatisfied.
	_, err = gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: actorType,
		ActorId:   wfID,
		Method:    "AddWorkflowEvent",
		Data:      dupBytes,
	})
	require.NoError(t, err)

	// The redelivery must NOT complete the workflow: it is the same single
	// event, so only the first wait is satisfied.
	require.Never(t, func() bool {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		return gerr == nil && resp.GetRuntimeStatus() == "COMPLETED"
	}, 3*time.Second, 200*time.Millisecond,
		"redelivered external event was applied twice and wrongly completed the workflow")

	// A genuinely distinct RaiseEvent (new ingestion timestamp) is NOT deduped
	// and satisfies the second wait, so the workflow completes.
	require.NoError(t, bc.RaiseEvent(ctx, api.InstanceID(wfID), "go", api.WithEventPayload("v2")))

	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"v1v2"`, meta.GetOutput().GetValue(),
		"distinct events must both be delivered (first wait=v1, second wait=v2)")
}
