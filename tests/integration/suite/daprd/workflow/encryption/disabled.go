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
	suite.Register(new(disabled))
}

// disabled turns encryption off while an encrypted workflow is running: the
// workflow cannot be loaded, since encryption is a one-way commitment, but it
// can still be force purged without the key.
type disabled struct {
	*fworkflow.Cluster
	daprd  *daprd.Daprd
	kekDir string
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.Cluster = fworkflow.NewCluster(t)
	d.kekDir = fworkflow.NewKEKDir(t, "kek.json")
	d.daprd = d.Daprd(t, fworkflow.EncryptionOptions(t, component, d.kekDir, "kek.json", false)...)
	return d.Options(d.daprd)
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	reg := fworkflow.WaitForEventRegistry(t, "enc-disabled", "go")
	id := fworkflow.ScheduleUntilPersisted(t, ctx, fworkflow.StartWorker(t, ctx, d.daprd, reg), d.DB, "enc-disabled", secret)

	off := d.Replace(t, ctx, d.daprd, fworkflow.EncryptionOptions(t, component, d.kekDir, "", false)...)
	cl := fworkflow.StartWorker(t, ctx, off, reg)
	_, err := cl.FetchWorkflowMetadata(ctx, id)
	require.ErrorContains(t, err, "encryption cannot be disabled")

	require.NoError(t, cl.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
	assert.Empty(t, fworkflow.StateRows(t, ctx, d.DB, id))
}
