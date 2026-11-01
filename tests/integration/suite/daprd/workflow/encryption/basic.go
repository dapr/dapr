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

package encryption

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(basic))
}

// basic runs a workflow carrying a secret in its input, an activity input
// and output, a failed activity's error, its custom status and a raised
// event. The secret never reaches the state store or the scheduler, every
// history row still decodes, and daprd returns plaintext to its clients.
type basic struct {
	*fworkflow.Cluster
	daprd   *daprd.Daprd
	release chan struct{}
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.release = make(chan struct{})
	b.Cluster = fworkflow.NewCluster(t)
	b.daprd = b.Daprd(t, fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "kek.json", false)...)
	return b.Options(b.daprd)
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("enc-basic", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		var charged string
		if err := ctx.CallActivity("charge", dworkflow.WithActivityInput(in)).Await(&charged); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("decline", dworkflow.WithActivityInput(in)).Await(nil); err == nil {
			return nil, errors.New("decline must fail")
		}
		ctx.SetCustomStatus(secret)
		var approval string
		if err := ctx.WaitForExternalEvent("approve", time.Minute).Await(&approval); err != nil {
			return nil, err
		}
		return charged + approval, nil
	}))
	require.NoError(t, reg.AddActivityN("charge", func(ctx dworkflow.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		<-b.release
		return in, nil
	}))
	require.NoError(t, reg.AddActivityN("decline", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, errors.New("declined " + secret)
	}))

	cl := fworkflow.StartWorker(t, ctx, b.daprd, reg)
	id, err := cl.ScheduleWorkflow(ctx, "enc-basic", dworkflow.WithInput(secret))
	require.NoError(t, err)

	// While the activity runs, its run-activity reminder is in the scheduler,
	// sealed.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var found bool
		for _, kv := range b.Scheduler.EtcdJobs(t, ctx) {
			if strings.Contains(string(kv.Key), "run-activity") {
				found = true
				assert.NotContains(c, string(kv.Value), secret)
			}
		}
		assert.True(c, found, "run-activity reminder not created")
	}, 20*time.Second, 50*time.Millisecond)
	close(b.release)

	require.NoError(t, cl.RaiseEvent(ctx, id, "approve", dworkflow.WithEventPayload(secret)))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Input.GetValue(), secret)
	assert.Contains(t, meta.Output.GetValue(), secret+secret)
	assert.Equal(t, secret, meta.CustomStatus.GetValue())

	w := fworkflow.RequireEncrypted(t, ctx, b.DB, b.Scheduler, id, secret)
	assert.Equal(t, component+"/kek.json", w.GetKid())
	assert.Zero(t, w.GetHistorySealedFrom())

	// Rows stay readable without the key: only payload fields are sealed.
	var names []string
	for name, raw := range fworkflow.StateRows(t, ctx, b.DB, id) {
		if !strings.HasPrefix(name, "history-") {
			continue
		}
		var e protos.HistoryEvent
		require.NoError(t, proto.Unmarshal(raw, &e), name)
		require.NotNil(t, e.GetTimestamp(), name)
		if ts := e.GetTaskScheduled(); ts != nil {
			names = append(names, ts.GetName())
		}
	}
	assert.ElementsMatch(t, []string{"charge", "decline"}, names)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	raw, err := proto.Marshal((*protos.GetInstanceHistoryResponse)(hist))
	require.NoError(t, err)
	assert.Contains(t, string(raw), secret, "daprd returns history in plaintext")
}
