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
	suite.Register(new(churnstrand))
}

// churnstrand duplicates turn completions while task listeners churn across
// hosts and requires every workflow to complete once a stable listener remains.
type churnstrand struct {
	workflow *workflow.Workflow
}

func (c *churnstrand) Setup(t *testing.T) []framework.Option {
	c.workflow = newClusteredFastPathDeployment(t, 3,
		"DAPR_WORKFLOW_TEST_DUPLICATE_TURN_COMPLETIONS", "40",
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *churnstrand) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

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
			time.Sleep(time.Millisecond * 250)
			return "ok", nil
		}))
		return r
	}

	dialAll := func() []*grpc.ClientConn {
		conns := make([]*grpc.ClientConn, 3)
		for i := range 3 {
			//nolint:staticcheck
			conn, err := grpc.DialContext(ctx, c.workflow.DaprN(i).GRPCAddress(),
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

	// Every phase is time-bounded so the whole run fits the suite's 45s
	// context even on a starved box: producers stop at quota or window end
	// (whichever first), the churn loop stops cycling at its soft deadline,
	// and the sweep spends whatever budget remains. Starts rotate across all
	// three daprds so a churning host does not starve a whole window.
	const perProducer = 12
	gclients := []rtv1.DaprClient{
		c.workflow.DaprN(0).GRPCClient(t, ctx),
		c.workflow.DaprN(1).GRPCClient(t, ctx),
		c.workflow.DaprN(2).GRPCClient(t, ctx),
	}
	var mu sync.Mutex
	var ids []string
	pctx, pcancel := context.WithTimeout(ctx, time.Second*18)
	defer pcancel()
	var pwg sync.WaitGroup
	for p := range 8 {
		pwg.Go(func() {
			for i, attempt := 0, 0; i < perProducer && pctx.Err() == nil; attempt++ {
				id := fmt.Sprintf("churnstrand-%d-%d", p, i)
				cctx, ccancel := context.WithTimeout(pctx, time.Second*2)
				_, err := gclients[attempt%len(gclients)].StartWorkflowBeta1(cctx, &rtv1.StartWorkflowRequest{
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
				case <-time.After(time.Millisecond * 50):
				}
			}
		})
	}

	churnDeadline := time.Now().Add(time.Second * 12)
	cycles := 0
	for cycle := 0; cycle < 12 && time.Now().Before(churnDeadline); cycle++ {
		cycles++
		conns := dialAll()
		lctx, lcancel := context.WithCancel(ctx)
		cl := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t,
			conns[0], conns[1], conns[2]), backend.DefaultLogger())
		require.NoError(t, cl.StartWorkItemListener(lctx, newRegistry()))
		wait := time.Millisecond * 300
		if cycle%2 == 0 {
			wait = time.Millisecond * 700
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
		lcancel()
		for _, conn := range conns {
			require.NoError(t, conn.Close())
		}
	}

	// Producers self-terminate at quota or at their window, so this wait is
	// bounded: on a fast box the full cohort starts, on a starved box the
	// cohort is smaller but every member is still swept to completion.
	pwg.Wait()

	mu.Lock()
	started := len(ids)
	mu.Unlock()
	require.Positive(t, started, "producer must have started a cohort")
	t.Logf("started %d workflows across %d churn cycles", started, cycles)

	conns := dialAll()
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	cl := client.NewTaskHubGrpcClient(fgrpc.LoadBalance(t,
		conns[0], conns[1], conns[2]), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, newRegistry()))

	// Sweep with whatever remains of the suite context, leaving grace for a
	// final fetch round and teardown before the hard 45s kill.
	ctxDeadline, ok := ctx.Deadline()
	require.True(t, ok, "suite context must carry a deadline")
	deadline := ctxDeadline.Add(-time.Second * 8)
	var stranded map[string]string
	for {
		stranded = map[string]string{}
		mu.Lock()
		snapshot := append([]string(nil), ids...)
		mu.Unlock()
		for _, id := range snapshot {
			require.NoError(t, ctx.Err(), "suite context expired mid-sweep; remaining statuses are unknowable, not stranded")
			fctx, fcancel := context.WithTimeout(ctx, time.Second*5)
			meta, err := cl.FetchWorkflowMetadata(fctx, api.InstanceID(id))
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
		"instances stranded non-terminal after churn stopped and a healthy worker reconnected (janitor-livelock)")
}
