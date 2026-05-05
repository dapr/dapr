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
	"errors"
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
	suite.Register(new(failedactivity))
}

type failedactivity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	daprd  *daprd.Daprd
	db     *sqlite.SQLite
}

func (f *failedactivity) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t)
	f.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	f.place = placement.New(t, placement.WithSentry(t, f.sentry))
	f.sched = scheduler.New(t, scheduler.WithSentry(f.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	f.daprd = daprd.New(t,
		daprd.WithSentry(t, f.sentry),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithScheduler(f.sched),
		daprd.WithResourceFiles(f.db.GetComponent(t)),
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
		framework.WithProcesses(f.sentry, f.db, f.place, f.sched, f.daprd),
	}
}

func (f *failedactivity) Run(t *testing.T, ctx context.Context) {
	f.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("attest-fail-activity-wf", func(ctx *dworkflow.WorkflowContext) (any, error) {
		_ = ctx.CallActivity("blowup").Await(nil)
		return "wf-done", nil
	})
	reg.AddActivityN("blowup", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, errors.New("activity blew up on purpose")
	})

	client := dworkflow.NewClient(f.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "attest-fail-activity-wf")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	atts := fworkflow.ActivityCompletionAttestations(t, ctx, f.db, id)
	require.Len(t, atts, 1)

	var payload protos.ActivityCompletionAttestationPayload
	require.NoError(t, proto.Unmarshal(atts[0].GetPayload(), &payload))

	assert.Equal(t, protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_FAILED, payload.GetTerminalStatus())
	assert.Equal(t, "blowup", payload.GetActivityName())
	assert.NotEmpty(t, payload.GetIoDigest())

	fworkflow.AssertSignerCertificateStripped(t, ctx, f.db, id)
}
