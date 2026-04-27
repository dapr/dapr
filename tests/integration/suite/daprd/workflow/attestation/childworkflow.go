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

package attestation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(childworkflow))
}

// childworkflow verifies that a parent workflow invoking a child workflow
// produces a ChildCompletionAttestation on the parent's stored
// ChildWorkflowInstanceCompleted event and absorbs the child's signer cert
// into the parent's ext-sigcert table.
type childworkflow struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (c *childworkflow) Setup(t *testing.T) []framework.Option {
	c.sentry = sentry.New(t)
	c.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	c.place = placement.New(t, placement.WithSentry(t, c.sentry))
	c.sched = scheduler.New(t, scheduler.WithSentry(c.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	c.daprd = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithScheduler(c.sched),
		daprd.WithResourceFiles(c.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: attest-on
spec:
  features:
  - name: WorkflowHistorySigning
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry, c.db, c.place, c.sched, c.daprd),
	}
}

func (c *childworkflow) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("attest-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("attest-child", dworkflow.WithChildWorkflowInput("ping")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
	reg.AddWorkflowN("attest-child", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "-pong", nil
	})

	client := dworkflow.NewClient(c.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "attest-parent")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, c.db, id, c.sentry.CABundle().X509.TrustAnchors)

	atts := fworkflow.ChildCompletionAttestations(t, ctx, c.db, id)
	require.Len(t, atts, 1, "expected one child completion attestation in the parent's history")

	var payload protos.ChildCompletionAttestationPayload
	require.NoError(t, proto.Unmarshal(atts[0].GetPayload(), &payload))

	assert.Equal(t, id, payload.GetParentInstanceId())
	assert.Equal(t, protos.TerminalStatus_TERMINAL_STATUS_COMPLETED, payload.GetTerminalStatus())
	assert.Equal(t, uint32(1), payload.GetCanonicalSpecVersion())
	assert.NotEmpty(t, payload.GetIoDigest())
	assert.NotEmpty(t, payload.GetSignerCertDigest())

	fworkflow.AssertSignerCertificateStripped(t, ctx, c.db, id)

	certs := fworkflow.ReadExtSigCerts(t, ctx, c.db, id)
	require.NotEmpty(t, certs)
	assert.Equal(t, payload.GetSignerCertDigest(), certs[0].GetDigest())
}
