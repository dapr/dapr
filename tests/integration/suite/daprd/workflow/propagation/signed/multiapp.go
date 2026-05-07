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

package signed

import (
	"context"
	"crypto/x509"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multiapp))
}

// multiapp is the signing-enabled counterpart to the unsigned cross-app
// propagation test in the parent package. The parent on App0 propagates its
// signed history to a child on App1; App1's daprd verifies the propagated
// chunk's signatures + cert against App0's SPIFFE identity before letting the
// child run, and absorbs App0's signing cert into its own ext-sigcert table.
type multiapp struct {
	workflow *procworkflow.Workflow

	childHistoryReceived atomic.Bool
	childTotalEvents     atomic.Int64
}

func (m *multiapp) Setup(t *testing.T) []framework.Option {
	m.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multiapp) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	app0Reg := m.workflow.Registry()
	app1Reg := m.workflow.RegistryN(1)

	app0Reg.AddWorkflowN("parentWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		remoteAppID := m.workflow.DaprN(1).AppID()
		var childResult string
		if err := ctx.CallChildWorkflow("remoteChildWorkflow",
			task.WithChildWorkflowInput("from-app0"),
			task.WithChildWorkflowAppID(remoteAppID),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&childResult); err != nil {
			return nil, err
		}
		return childResult, nil
	})

	app1Reg.AddWorkflowN("remoteChildWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph == nil {
			return statusNoHistory, nil
		}
		m.childHistoryReceived.Store(true)
		m.childTotalEvents.Store(int64(len(ph.Events())))
		return statusDone, nil
	})

	client0 := m.workflow.BackendClient(t, ctx)
	m.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "parentWorkflow", api.WithInput("test"))
	require.NoError(t, err)

	metadata, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata),
		"parent workflow should complete; signed propagated history must be accepted by App1")

	require.True(t, m.childHistoryReceived.Load(), "child must observe propagated history once verified")
	assert.Positive(t, m.childTotalEvents.Load())

	// App1 absorbed App0's signing cert into ext-sigcert during chunk
	// verification on receive. Verify the cert's SPIFFE identity matches App0
	// (the producer): this is the cross-identity property the
	// propagation-signing mechanism exists to prove.
	childInstance := fworkflow.ChildInstanceIDFromHistory(t, ctx, m.workflow.DB(), string(id))
	require.NotEmpty(t, childInstance, "should find child instance ID in parent history")

	extCerts := fworkflow.ReadExtSigCerts(t, ctx, m.workflow.DB(), childInstance)
	require.Len(t, extCerts, 1,
		"child workflow should have absorbed exactly one foreign signing cert (App0's)")

	parsed, err := x509.ParseCertificates(extCerts[0].GetCertificate())
	require.NoError(t, err)
	require.NotEmpty(t, parsed[0].URIs)
	sid, err := spiffeid.FromURI(parsed[0].URIs[0])
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(sid.Path(), "/"+m.workflow.Dapr().AppID()),
		"absorbed foreign cert SPIFFE ID should be App0's, got %q", sid.String())
}
