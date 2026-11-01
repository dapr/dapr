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
	suite.Register(new(rotation))
}

// rotation switches to a new key encryption key while a workflow runs. Its
// next save re-wraps the instance key under the new one, and the rows sealed
// before still load.
type rotation struct {
	*fworkflow.Cluster
	daprd  *daprd.Daprd
	kekDir string
}

func (r *rotation) Setup(t *testing.T) []framework.Option {
	r.Cluster = fworkflow.NewCluster(t)
	r.kekDir = fworkflow.NewKEKDir(t, "kek.json", "kek2.json")
	r.daprd = r.Daprd(t, fworkflow.EncryptionOptions(t, component, r.kekDir, "kek.json", false)...)
	return r.Options(r.daprd)
}

func (r *rotation) Run(t *testing.T, ctx context.Context) {
	reg := fworkflow.WaitForEventRegistry(t, "enc-rotation", "go")
	id := fworkflow.ScheduleUntilPersisted(t, ctx, fworkflow.StartWorker(t, ctx, r.daprd, reg), r.DB, "enc-rotation", secret)
	assert.Equal(t, component+"/kek.json", fworkflow.RequireEncrypted(t, ctx, r.DB, r.Scheduler, id, secret).GetKid())

	d := r.Replace(t, ctx, r.daprd, fworkflow.EncryptionOptions(t, component, r.kekDir, "kek2.json", false)...)
	cl := fworkflow.StartWorker(t, ctx, d, reg)
	require.NoError(t, cl.RaiseEvent(ctx, id, "go", dworkflow.WithEventPayload(secret)))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Output.GetValue(), secret+secret)

	assert.Equal(t, component+"/kek2.json", fworkflow.RequireEncrypted(t, ctx, r.DB, r.Scheduler, id, secret).GetKid())
}
