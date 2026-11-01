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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(enablesigned))
}

// enablesigned turns encryption on for a running signed workflow. Its signed
// history rows are kept as written, so the signature chain still verifies,
// and every row written after is sealed.
type enablesigned struct {
	*fworkflow.Cluster
	daprd  *daprd.Daprd
	kekDir string
}

func (e *enablesigned) Setup(t *testing.T) []framework.Option {
	e.Cluster = fworkflow.NewCluster(t)
	e.kekDir = fworkflow.NewKEKDir(t, "kek.json")
	e.daprd = e.Daprd(t, fworkflow.EncryptionOptions(t, component, e.kekDir, "", true)...)
	return e.Options(e.daprd)
}

func (e *enablesigned) Run(t *testing.T, ctx context.Context) {
	reg := fworkflow.WaitForEventRegistry(t, "enc-enablesigned", "go")
	id := fworkflow.ScheduleUntilPersisted(t, ctx, fworkflow.StartWorker(t, ctx, e.daprd, reg), e.DB, "enc-enablesigned", secret)
	plainRows := 0
	for name := range fworkflow.StateRows(t, ctx, e.DB, id) {
		if strings.HasPrefix(name, "history-") {
			plainRows++
		}
	}
	require.Positive(t, plainRows)

	d := e.Replace(t, ctx, e.daprd, fworkflow.EncryptionOptions(t, component, e.kekDir, "kek.json", true)...)
	cl := fworkflow.StartWorker(t, ctx, d, reg)
	require.NoError(t, cl.RaiseEvent(ctx, id, "go", dworkflow.WithEventPayload(secret)))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id, dworkflow.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Contains(t, meta.Output.GetValue(), secret+secret)

	rows := fworkflow.StateRows(t, ctx, e.DB, id)
	var w internalsv1pb.WorkflowEncryptionKey
	require.NoError(t, proto.Unmarshal(rows["encryption"], &w))
	assert.Equal(t, uint64(plainRows), w.GetHistorySealedFrom())
	for name, v := range rows {
		var i uint64
		if _, err := fmt.Sscanf(name, "history-%06d", &i); err == nil && i < w.GetHistorySealedFrom() {
			continue
		}
		assert.NotContains(t, string(v), secret, "row %s written after enabling holds the plaintext", name)
	}
	fworkflow.VerifySignatureChain(t, ctx, e.DB, id, e.Sentry.CABundle().X509.TrustAnchors)
}
