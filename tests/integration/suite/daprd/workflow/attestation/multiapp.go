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
	"crypto/x509"
	"strings"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
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
	suite.Register(new(multiapp))
}

type multiapp struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	db     *sqlite.SQLite
}

func (m *multiapp) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)
	m.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	m.place = placement.New(t, placement.WithSentry(t, m.sentry))
	m.sched = scheduler.New(t, scheduler.WithSentry(m.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	m.daprd1 = daprd.New(t,
		daprd.WithAppID("attest-parent-app"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
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

	m.daprd2 = daprd.New(t,
		daprd.WithAppID("attest-child-app"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
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
		framework.WithProcesses(m.sentry, m.db, m.place, m.sched, m.daprd1, m.daprd2),
	}
}

func (m *multiapp) Run(t *testing.T, ctx context.Context) {
	m.daprd1.WaitUntilRunning(t, ctx)
	m.daprd2.WaitUntilRunning(t, ctx)

	regParent := dworkflow.NewRegistry()
	regParent.AddWorkflowN("attest-xapp-parent", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("remote-act",
			dworkflow.WithActivityAppID(m.daprd2.AppID()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	regChild := dworkflow.NewRegistry()
	regChild.AddActivityN("remote-act", func(ctx dworkflow.ActivityContext) (any, error) {
		return "from-child-app", nil
	})

	client1 := dworkflow.NewClient(m.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, regParent))

	client2 := dworkflow.NewClient(m.daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, regChild))

	id, err := client1.ScheduleWorkflow(ctx, "attest-xapp-parent")
	require.NoError(t, err)

	_, err = client1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, m.db, id, m.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, "attest-parent-app")

	atts := fworkflow.ActivityCompletionAttestations(t, ctx, m.db, id)
	require.Len(t, atts, 1)

	var payload protos.ActivityCompletionAttestationPayload
	require.NoError(t, proto.Unmarshal(atts[0].GetPayload(), &payload))

	certs := fworkflow.ReadExtSigCerts(t, ctx, m.db, id)
	require.Len(t, certs, 1)
	assert.Equal(t, payload.GetSignerCertDigest(), certs[0].GetDigest())

	parsed, err := x509.ParseCertificates(certs[0].GetCertificate())
	require.NoError(t, err)
	require.NotEmpty(t, parsed)
	require.NotEmpty(t, parsed[0].URIs)
	sid, err := spiffeid.FromURI(parsed[0].URIs[0])
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(sid.Path(), "/attest-child-app"))
}
