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
	suite.Register(new(lineage))
}

// lineage is the signing-enabled counterpart to multiapplineage: A→B→C with
// PropagateLineage. The leaf (App2) receives propagated history containing
// chunks from both App0 and App1. Each chunk must be signed by its respective
// producer. The leaf's ext-sigcert table ends up with two foreign certs (one
// per upstream app), each carrying the producer's SPIFFE identity.
type lineage struct {
	workflow *procworkflow.Workflow

	leafReceived atomic.Bool
}

func (s *lineage) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *lineage) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)
	app2Reg := s.workflow.RegistryN(2)

	app0Reg.AddWorkflowN("lineRoot", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("lineMiddle",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&r)
	})

	app1Reg.AddWorkflowN("lineMiddle", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("lineLeaf",
			task.WithChildWorkflowAppID(s.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&r)
	})

	app2Reg.AddWorkflowN("lineLeaf", func(ctx *task.WorkflowContext) (any, error) {
		if ph := ctx.GetPropagatedHistory(); ph != nil {
			s.leafReceived.Store(true)
		}
		return statusDone, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)
	s.workflow.BackendClientN(t, ctx, 2)

	id, err := client0.ScheduleNewWorkflow(ctx, "lineRoot")
	require.NoError(t, err)

	meta, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta),
		"root workflow must complete; signed propagation must verify at every hop")
	assert.True(t, s.leafReceived.Load(),
		"leaf workflow on App2 must observe propagated history once both A and B chunks verify")

	// Find the leaf workflow's instance ID by walking the parent chain.
	middleID := fworkflow.ChildInstanceIDFromHistory(t, ctx, s.workflow.DB(), string(id))
	require.NotEmpty(t, middleID, "should find App1 middle's instance ID in App0's history")
	leafID := fworkflow.ChildInstanceIDFromHistory(t, ctx, s.workflow.DB(), middleID)
	require.NotEmpty(t, leafID, "should find App2 leaf's instance ID in App1's history")

	// Leaf's ext-sigcert must contain BOTH App0 and App1 foreign certs.
	extCerts := fworkflow.ReadExtSigCerts(t, ctx, s.workflow.DB(), leafID)
	require.Len(t, extCerts, 2,
		"leaf workflow should absorb foreign certs from both upstream chunks (App0, App1)")

	gotApps := make(map[string]bool)
	for _, c := range extCerts {
		parsed, err := x509.ParseCertificates(c.GetCertificate())
		require.NoError(t, err)
		require.NotEmpty(t, parsed[0].URIs)
		sid, err := spiffeid.FromURI(parsed[0].URIs[0])
		require.NoError(t, err)
		// SPIFFE path is /ns/<namespace>/<app-id>; take the last
		// segment instead of slicing on a hard-coded prefix so the
		// test doesn't panic if the namespace differs from default
		// or the format ever changes.
		segs := strings.Split(sid.Path(), "/")
		require.NotEmpty(t, segs)
		gotApps[segs[len(segs)-1]] = true
	}
	assert.True(t, gotApps[s.workflow.Dapr().AppID()],
		"App0's identity should be among absorbed foreign certs")
	assert.True(t, gotApps[s.workflow.DaprN(1).AppID()],
		"App1's identity should be among absorbed foreign certs")
}
