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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(enable))
}

// enable starts a workflow with encryption off, then turns it on. The next
// save rewrites the instance's existing rows sealed.
type enable struct {
	*fworkflow.Cluster
	daprd  *daprd.Daprd
	kekDir string
}

func (e *enable) Setup(t *testing.T) []framework.Option {
	e.Cluster = fworkflow.NewCluster(t)
	e.kekDir = fworkflow.NewKEKDir(t, "kek.json")
	e.daprd = e.Daprd(t, fworkflow.EncryptionOptions(t, component, e.kekDir, "", false)...)
	return e.Options(e.daprd)
}

func (e *enable) Run(t *testing.T, ctx context.Context) {
	reg := fworkflow.WaitForEventRegistry(t, "enc-enable", "go")
	id := fworkflow.ScheduleUntilPersisted(t, ctx, fworkflow.StartWorker(t, ctx, e.daprd, reg), e.DB, "enc-enable", secret)
	require.True(t, fworkflow.StateContains(t, ctx, e.DB, id, secret), "encryption off stores plaintext")
	require.NotContains(t, fworkflow.StateRows(t, ctx, e.DB, id), "encryption")

	d := e.Replace(t, ctx, e.daprd, fworkflow.EncryptionOptions(t, component, e.kekDir, "kek.json", false)...)
	cl := fworkflow.StartWorker(t, ctx, d, reg)
	require.NoError(t, cl.RaiseEvent(ctx, id, "go", dworkflow.WithEventPayload(secret)))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Output.GetValue(), secret+secret)

	assert.Zero(t, fworkflow.RequireEncrypted(t, ctx, e.DB, e.Scheduler, id, secret).GetHistorySealedFrom())
}
