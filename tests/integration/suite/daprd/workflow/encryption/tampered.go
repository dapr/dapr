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
	"strings"
	"testing"

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
	suite.Register(new(tampered))
}

// tampered modifies a sealed payload in the state store. The workflow no
// longer loads: authenticated encryption detects the change without
// history signing.
type tampered struct {
	*fworkflow.Cluster
	daprd *daprd.Daprd
}

func (tp *tampered) Setup(t *testing.T) []framework.Option {
	tp.Cluster = fworkflow.NewCluster(t)
	tp.daprd = tp.Daprd(t, fworkflow.EncryptionOptions(t, component, fworkflow.NewKEKDir(t, "kek.json"), "kek.json", false)...)
	return tp.Options(tp.daprd)
}

func (tp *tampered) Run(t *testing.T, ctx context.Context) {
	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("enc-tampered", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := fworkflow.StartWorker(t, ctx, tp.daprd, reg)
	id, err := cl.ScheduleWorkflow(ctx, "enc-tampered", dworkflow.WithInput(secret))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	var tamperedRow bool
	for name, raw := range fworkflow.StateRows(t, ctx, tp.DB, id) {
		if !strings.HasPrefix(name, "history-") {
			continue
		}
		var e protos.HistoryEvent
		require.NoError(t, proto.Unmarshal(raw, &e))
		in := e.GetExecutionStarted().GetInput()
		if in == nil {
			continue
		}
		in.Value = in.GetValue()[:len(in.GetValue())-4] + "AAAA"
		b, merr := proto.Marshal(&e)
		require.NoError(t, merr)
		key, _ := tp.DB.ReadStateValue(t, ctx, id, name)
		tp.DB.WriteStateValue(t, ctx, key, b)
		tamperedRow = true
	}
	require.True(t, tamperedRow)

	tp.daprd.Restart(t, ctx)
	_, err = fworkflow.StartWorker(t, ctx, tp.daprd, reg).FetchWorkflowMetadata(ctx, id)
	require.ErrorContains(t, err, "encrypted payload verification failed")
}
