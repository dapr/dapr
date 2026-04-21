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

package signing

import (
	"context"
	"crypto/x509"
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

// multiapp verifies that when two daprd instances share the same state store
// and the orchestrator runs on one instance, the signatures are attributed to
// the orchestrator's app ID.
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
		daprd.WithAppID("sign-xapp-orch"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
	)

	m.daprd2 = daprd.New(t,
		daprd.WithAppID("sign-xapp-act"),
		daprd.WithSentry(t, m.sentry),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithScheduler(m.sched),
		daprd.WithResourceFiles(m.db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
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

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-xapp", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("remote-noop").Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	reg.AddActivityN("remote-noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	// Both instances register the same workflow and activity.
	client1 := dworkflow.NewClient(m.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client1.StartWorker(ctx, reg))

	client2 := dworkflow.NewClient(m.daprd2.GRPCConn(t, ctx))
	require.NoError(t, client2.StartWorker(ctx, reg))

	// Schedule on the orchestrator instance.
	id, err := client1.ScheduleWorkflow(ctx, "sign-xapp")
	require.NoError(t, err)

	_, err = client1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	fworkflow.VerifySignatureChain(t, ctx, m.db, id, m.sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, m.db, id, m.daprd1.AppID())

	// Negative check: no certificate should be attributed to daprd2's app ID
	// (activity-only app). Signing is always tied to the orchestrator identity.
	certValues := m.db.ReadStateValues(t, ctx, id, "sigcert")
	require.NotEmpty(t, certValues)
	unexpected := spiffeid.RequireFromSegments(
		spiffeid.RequireTrustDomainFromString("public"),
		"ns", "default", m.daprd2.AppID(),
	)
	for i, raw := range certValues {
		var sc protos.SigningCertificate
		require.NoError(t, proto.Unmarshal(raw, &sc))
		certs, err := x509.ParseCertificates(sc.GetCertificate())
		require.NoError(t, err)
		require.NotEmpty(t, certs[0].URIs)
		id, err := spiffeid.FromURI(certs[0].URIs[0])
		require.NoError(t, err)
		assert.NotEqual(t, unexpected, id, "certificate %d unexpectedly attributed to daprd2", i)
	}
}
