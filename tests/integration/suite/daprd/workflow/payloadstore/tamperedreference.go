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

package payloadstore

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	wfpayloadstore "github.com/dapr/durabletask-go/backend/payloadstore"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(tamperedreference))
}

// tamperedreference pins the security property history signing adds to
// payload offloading. The payload blob itself is bound by the checksum
// inside the reference, so the one attack the checksum cannot catch is
// swapping the whole reference in the state store for a well-formed one
// pointing at different attacker-known content - such a forgery passes
// checksum verification by construction. The reference bytes are part of
// the signed history event, so signing must detect the swap: on the next
// cold load the workflow is terminally failed as tampered instead of
// resolving the forged reference.
type tamperedreference struct {
	workflow *workflow.Workflow
}

func (m *tamperedreference) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *tamperedreference) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	input := "tamper-input-marker-" + strings.Repeat("x", 4096)

	m.workflow.Registry().AddWorkflowN("victim", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("continue", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	client := m.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "victim", api.WithInput(input))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	// The signed history carries the input as a reference.
	events := fworkflow.ReadHistoryEvents(t, ctx, m.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload), input)

	// Swap the reference for a well-formed one addressing content whose
	// checksum the attacker knows. Checksum verification alone would
	// accept this on dereference.
	attackerContent := []byte(`"attacker-known-content"`)
	forged := wfpayloadstore.EncodeReference(wfpayloadstore.Reference{
		Checksum: sha256.Sum256(attackerContent),
		Key:      "attacker-blob",
		Size:     uint64(len(attackerContent)),
	})
	fworkflow.MutateHistoryEvent(t, ctx, m.workflow.DB(), string(id), func(e *protos.HistoryEvent) bool {
		es := e.GetExecutionStarted()
		if es == nil || es.GetInput() == nil {
			return false
		}
		es.Input = wrapperspb.String(forged)
		return true
	})

	// Restart daprd to drop the in-memory cache, then trigger the
	// orchestrator actor. The cold load must detect the signature
	// mismatch and terminally fail the workflow as tampered.
	m.workflow.Dapr().Restart(t, ctx)
	m.workflow.Dapr().WaitUntilRunning(t, ctx)

	client = m.workflow.BackendClient(t, ctx)
	require.NoError(t, client.RaiseEvent(ctx, id, "continue"))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client.FetchWorkflowMetadata(ctx, id)
		if !assert.NoError(c, err) || !assert.NotNil(c, meta) {
			return
		}
		if !assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus()) {
			return
		}
		assert.Equal(c, wferrors.ErrorTypeHistoryTampered, meta.GetFailureDetails().GetErrorType())
	}, time.Second*10, time.Millisecond*10)
}
