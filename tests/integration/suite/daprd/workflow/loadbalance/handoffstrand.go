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

package loadbalance

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fgrpc "github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(handoffstrand))
}

// handoffstrand strands a fold-held completion at a rolling restart's
// placement handoff and asserts the janitor recovers every instance.
type handoffstrand struct {
	workflow *workflow.Workflow
}

func (h *handoffstrand) Setup(t *testing.T) []framework.Option {
	h.workflow = newClusteredFastPathDeployment(t, 3,
		"DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES", "100000",
	)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *handoffstrand) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	newRegistry := func() *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("Seq", func(wc *task.WorkflowContext) (any, error) {
			var out string
			for range 3 {
				if err := wc.CallActivity("Echo", task.WithActivityInput("x")).Await(&out); err != nil {
					return nil, err
				}
			}
			return out, nil
		}))
		require.NoError(t, r.AddActivityN("Echo", func(task.ActivityContext) (any, error) {
			return "ok", nil
		}))
		return r
	}

	dialAll := func() []*grpc.ClientConn {
		conns := make([]*grpc.ClientConn, 3)
		for i := range 3 {
			//nolint:staticcheck
			conn, err := grpc.DialContext(ctx, h.workflow.DaprN(i).GRPCAddress(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				//nolint:staticcheck
				grpc.WithBlock(),
				grpc.WithDefaultCallOptions(
					grpc.MaxCallRecvMsgSize(math.MaxInt32),
					grpc.MaxCallSendMsgSize(math.MaxInt32),
				),
			)
			require.NoError(t, err)
			conns[i] = conn
		}
		return conns
	}

	gclient := h.workflow.DaprN(0).GRPCClient(t, ctx)
	var mu sync.Mutex
	var ids []string
	pctx, pcancel := context.WithCancel(ctx)
	var pwg sync.WaitGroup
	for p := range 4 {
		pwg.Go(func() {
			i := 0
			for pctx.Err() == nil {
				id := fmt.Sprintf("handoffstrand-%d-%d", p, i)
				cctx, ccancel := context.WithTimeout(pctx, time.Second*2)
				_, err := gclient.StartWorkflowBeta1(cctx, &rtv1.StartWorkflowRequest{
					WorkflowComponent: "dapr",
					WorkflowName:      "Seq",
					InstanceId:        id,
				})
				ccancel()
				if err == nil {
					mu.Lock()
					ids = append(ids, id)
					mu.Unlock()
					i++
				}
				select {
				case <-pctx.Done():
				case <-time.After(time.Millisecond * 25):
				}
			}
		})
	}

	waitForCohort := func(target int) {
		deadline := time.Now().Add(time.Second * 20)
		for ctx.Err() == nil && time.Now().Before(deadline) {
			mu.Lock()
			n := len(ids)
			mu.Unlock()
			if n >= target {
				return
			}
			time.Sleep(time.Millisecond * 50)
		}
	}

	conns1 := dialAll()
	lctx, lcancel := context.WithCancel(ctx)
	cl1 := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t,
		conns1[0], conns1[1], conns1[2]), backend.DefaultLogger())
	require.NoError(t, cl1.StartWorkItemListener(lctx, newRegistry()))
	waitForCohort(15)

	lcancel()
	for _, conn := range conns1 {
		require.NoError(t, conn.Close())
	}
	h.workflow.DaprN(2).Restart(t, ctx)
	h.workflow.DaprN(2).WaitUntilRunning(t, ctx)

	conns2 := dialAll()
	t.Cleanup(func() {
		for _, conn := range conns2 {
			_ = conn.Close()
		}
	})
	cl2 := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t,
		conns2[0], conns2[1], conns2[2]), backend.DefaultLogger())
	require.NoError(t, cl2.StartWorkItemListener(ctx, newRegistry()))

	mu.Lock()
	preRebalance := len(ids)
	mu.Unlock()
	waitForCohort(preRebalance + 15)
	pcancel()
	pwg.Wait()

	mu.Lock()
	started := len(ids)
	mu.Unlock()
	require.Positive(t, started, "producer must have started a cohort")
	t.Logf("started %d workflows across the handoff window", started)

	deadline := time.Now().Add(time.Second * 90)
	var stranded map[string]string
	for {
		stranded = map[string]string{}
		mu.Lock()
		snapshot := append([]string(nil), ids...)
		mu.Unlock()
		for _, id := range snapshot {
			fctx, fcancel := context.WithTimeout(ctx, time.Second*5)
			meta, err := cl2.FetchWorkflowMetadata(fctx, api.InstanceID(id))
			fcancel()
			if err != nil || meta == nil {
				stranded[id] = fmt.Sprintf("fetch failed: %v", err)
				continue
			}
			if !api.WorkflowMetadataIsComplete(meta) {
				stranded[id] = meta.GetRuntimeStatus().String()
			}
		}
		if len(stranded) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second * 2)
	}

	for id, status := range stranded {
		t.Logf("STRANDED instance %s status=%s", id, status)
	}
	assert.Empty(t, stranded,
		"instances stranded non-terminal past the janitor recovery window (captive fold-held completions)")
}
