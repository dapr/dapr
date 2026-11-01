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
	suite.Register(new(signed))
}

// signed runs an encrypted workflow with history signing on. The stored
// rows hold no plaintext, and the signature chain verifies over them without
// the payload key.
type signed struct {
	*fworkflow.Cluster
	daprd *daprd.Daprd
}

func (s *signed) Setup(t *testing.T) []framework.Option {
	s.Cluster = fworkflow.NewCluster(t)
	s.daprd = s.Daprd(t, fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "kek.json", true)...)
	return s.Options(s.daprd)
}

func (s *signed) Run(t *testing.T, ctx context.Context) {
	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("enc-signed", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var in, out string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		err := ctx.CallActivity("echo", dworkflow.WithActivityInput(in)).Await(&out)
		return out, err
	}))
	require.NoError(t, reg.AddActivityN("echo", func(ctx dworkflow.ActivityContext) (any, error) {
		var in string
		err := ctx.GetInput(&in)
		return in, err
	}))
	cl := fworkflow.StartWorker(t, ctx, s.daprd, reg)

	id, err := cl.ScheduleWorkflow(ctx, "enc-signed", dworkflow.WithInput(secret))
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Output.GetValue(), secret)

	fworkflow.RequireEncrypted(t, ctx, s.DB, s.Scheduler, id, secret)
	fworkflow.VerifySignatureChain(t, ctx, s.DB, id, s.Sentry.CABundle().X509.TrustAnchors)
	fworkflow.VerifyCertAppID(t, ctx, s.DB, id, s.daprd.AppID())
}
