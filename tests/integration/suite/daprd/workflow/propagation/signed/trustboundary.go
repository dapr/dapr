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
	suite.Register(new(trustboundary))
}

// trustboundary verifies signing under PropagateOwnHistory: the middle
// workflow receives App0's chunk under Lineage but only forwards its OWN chunk
// to the leaf (no upstream lineage). The leaf then sees just one chunk from
// App1, signed by App1's identity. This proves the trust-boundary scope honors
// signing semantics: the leaf's ext-sigcert contains only the immediate
// sender's foreign cert, not App0's.
type trustboundary struct {
	workflow *procworkflow.Workflow

	leafChunkCount atomic.Int64
}

func (s *trustboundary) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *trustboundary) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)
	app2Reg := s.workflow.RegistryN(2)

	app0Reg.AddWorkflowN("tbRoot", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("tbMiddle",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&r)
	})

	// Middle uses PropagateOwnHistory: only its events go to the leaf;
	// App0's chunk does NOT cross the boundary.
	app1Reg.AddWorkflowN("tbMiddle", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("tbLeaf",
			task.WithChildWorkflowAppID(s.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&r)
	})

	app2Reg.AddWorkflowN("tbLeaf", func(ctx *task.WorkflowContext) (any, error) {
		if ph := ctx.GetPropagatedHistory(); ph != nil {
			s.leafChunkCount.Store(int64(len(ph.GetWorkflows())))
		}
		return statusDone, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)
	s.workflow.BackendClientN(t, ctx, 2)

	id, err := client0.ScheduleNewWorkflow(ctx, "tbRoot")
	require.NoError(t, err)

	meta, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))

	assert.Equal(t, int64(1), s.leafChunkCount.Load(),
		"under OwnHistory, leaf should see exactly one chunk (App1's), not App0's")

	// Leaf's ext-sigcert holds only App1's cert (the immediate sender).
	middleID := fworkflow.ChildInstanceIDFromHistory(t, ctx, s.workflow.DB(), string(id))
	require.NotEmpty(t, middleID)
	leafID := fworkflow.ChildInstanceIDFromHistory(t, ctx, s.workflow.DB(), middleID)
	require.NotEmpty(t, leafID)

	extCerts := fworkflow.ReadExtSigCerts(t, ctx, s.workflow.DB(), leafID)
	require.Len(t, extCerts, 1, "leaf should only absorb App1's foreign cert under trust-boundary scope")

	parsed, err := x509.ParseCertificates(extCerts[0].GetCertificate())
	require.NoError(t, err)
	require.NotEmpty(t, parsed[0].URIs)
	sid, err := spiffeid.FromURI(parsed[0].URIs[0])
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(sid.Path(), "/"+s.workflow.DaprN(1).AppID()),
		"leaf's foreign cert must be App1's (the immediate sender), got %q", sid.String())
}
