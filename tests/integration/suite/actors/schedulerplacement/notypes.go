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

package schedulerplacement

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notypes))
}

// notypes runs a sidecar hosting no actor types under scheduler placement:
// it still connects to placement and its actor calls land on the hosting
// sidecar.
type notypes struct {
	sched  *scheduler.Scheduler
	host   *daprd.Daprd
	caller *daprd.Daprd

	invoked atomic.Int64
}

func (n *notypes) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		n.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	n.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	n.host = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(n.sched),
	)
	n.caller = daprd.New(t,
		daprd.WithScheduler(n.sched),
	)

	return []framework.Option{
		framework.WithProcesses(n.sched, srv, n.host, n.caller),
	}
}

func (n *notypes) Run(t *testing.T, ctx context.Context) {
	n.sched.WaitUntilRunning(t, ctx)
	n.host.WaitUntilRunning(t, ctx)
	n.caller.WaitUntilRunning(t, ctx)

	client := n.caller.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, n.invoked.Load())
}
