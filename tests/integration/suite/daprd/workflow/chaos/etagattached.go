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

package chaos

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(etagattached))
}

// etagattached verifies that the orchestrator's transactional save attaches
// the cached metadata ETag to the metadata row's Upsert on every save after
// the first. This is the optimistic-concurrency anchor that prevents a
// split-brain peer write from clobbering history rows: a stale ETag aborts the
// entire Multi.
type etagattached struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *etagattached) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	e.store = fault.New(t)

	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)

	e.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, e.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(e.ss, e.workflow),
	}
}

func (e *etagattached) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "etagattached-wf"

	var (
		mu         sync.Mutex
		metaEtags  []*string
		wantSuffix = wfID + "||metadata"
	)
	e.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		mu.Lock()
		defer mu.Unlock()
		for _, op := range req.Operations {
			s, ok := op.(state.SetRequest)
			if !ok || !strings.HasSuffix(s.Key, wantSuffix) {
				continue
			}
			if s.ETag == nil {
				metaEtags = append(metaEtags, nil)
			} else {
				v := *s.ETag
				metaEtags = append(metaEtags, &v)
			}
		}
	})

	r := e.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		return "done", nil
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	bc := e.workflow.BackendClient(t, ctx)
	gclient := e.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(metaEtags), 2, "expected at least two metadata Upserts")
	assert.Nil(t, metaEtags[0], "first metadata Upsert (workflow create) must have no ETag")
	hasEtag := false
	for _, e := range metaEtags[1:] {
		if e != nil && *e != "" {
			hasEtag = true
			break
		}
	}
	assert.True(t, hasEtag,
		"at least one post-create metadata Upsert must carry an ETag for optimistic concurrency")
}
