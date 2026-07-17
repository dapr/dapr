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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(signing))
}

// signing asserts that history signing covers the reference-carrying
// bytes: with both signing and payload offloading enabled, the persisted
// signature chain verifies over the persisted rows, and a daprd restart —
// which forces a cold reload with full signature verification inside
// daprd — leaves the workflow readable and COMPLETED instead of
// tombstoning it as tampered.
type signing struct {
	workflow *workflow.Workflow
}

func (s *signing) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *signing) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	input := "signed-input-marker-" + strings.Repeat("s", 4096)

	s.workflow.Registry().AddWorkflowN("signed", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "signed", api.WithInput(input))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The persisted input is a reference, and the signature chain
	// verifies over the persisted (reference-carrying) rows.
	events := fworkflow.ReadHistoryEvents(t, ctx, s.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload), input)
	fworkflow.VerifySignatureChain(t, ctx, s.workflow.DB(), string(id),
		s.workflow.Sentry().CABundle().X509.TrustAnchors)

	// Restart daprd to drop the in-memory cache; the next access reloads
	// from the store and runs signature verification over the
	// reference-carrying history inside daprd. A signature computed over
	// inline payloads would fail here and tombstone the workflow.
	s.workflow.Dapr().Restart(t, ctx)
	s.workflow.Dapr().WaitUntilRunning(t, ctx)

	client = s.workflow.BackendClient(t, ctx)
	meta, err = client.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"workflow must remain COMPLETED after a cold reload verifies signatures over offloaded history")
}
