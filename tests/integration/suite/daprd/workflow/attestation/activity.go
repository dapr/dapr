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
	suite.Register(new(activity))
}

type activity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)
	a.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	a.place = placement.New(t, placement.WithSentry(t, a.sentry))
	a.sched = scheduler.New(t, scheduler.WithSentry(a.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	a.daprd = daprd.New(t,
		daprd.WithSentry(t, a.sentry),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithScheduler(a.sched),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
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
		framework.WithProcesses(a.sentry, a.db, a.place, a.sched, a.daprd),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("attest-activity", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		if err := ctx.CallActivity("say", dworkflow.WithActivityInput("hello")).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	reg.AddActivityN("say", func(ctx dworkflow.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "!", nil
	})

	client := dworkflow.NewClient(a.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "attest-activity")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Signed history must still verify end to end.
	fworkflow.VerifySignatureChain(t, ctx, a.db, id, a.sentry.CABundle().X509.TrustAnchors)

	// Exactly one activity completion attestation should be stored, with
	// the correct parent binding and terminal status.
	atts := fworkflow.ActivityCompletionAttestations(t, ctx, a.db, id)
	require.Len(t, atts, 1, "expected one activity completion attestation")

	var payload protos.ActivityCompletionAttestationPayload
	require.NoError(t, proto.Unmarshal(atts[0].GetPayload(), &payload))

	assert.Equal(t, id, payload.GetParentInstanceId())
	assert.Equal(t, "say", payload.GetActivityName())
	assert.Equal(t, protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_COMPLETED, payload.GetTerminalStatus())
	assert.Equal(t, uint32(1), payload.GetCanonicalSpecVersion())
	assert.NotEmpty(t, payload.GetIoDigest())
	assert.NotEmpty(t, payload.GetSignerCertDigest())

	// Companion signer cert must be stripped from the stored event — it
	// lives in ext-sigcert.
	fworkflow.AssertSignerCertificateStripped(t, ctx, a.db, id)

	// Exactly one ext-sigcert entry should exist (the activity executor's
	// cert). In a same-app workflow it is the same SPIFFE identity as the
	// parent's own sigcert, but content-addressed foreign storage is a
	// separate table by design.
	certs := fworkflow.ReadExtSigCerts(t, ctx, a.db, id)
	require.Len(t, certs, 1)
	assert.Equal(t, payload.GetSignerCertDigest(), certs[0].GetDigest(),
		"ext-sigcert digest must match the attestation's signerCertDigest")
}
