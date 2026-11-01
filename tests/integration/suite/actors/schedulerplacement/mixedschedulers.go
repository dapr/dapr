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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mixedschedulers))
}

// mixedschedulers runs a scheduler cluster where only two of the three
// schedulers serve placement, as during a rolling upgrade: the sidecar
// follows the advertised leader and its actors work.
type mixedschedulers struct {
	cluster *cluster.Cluster
	daprd   *daprd.Daprd

	invoked atomic.Int64
}

func (m *mixedschedulers) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		m.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	m.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerNOptions(0, scheduler.WithPlacementEnabled(true)),
		cluster.WithSchedulerNOptions(1, scheduler.WithPlacementEnabled(true)),
	)
	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(m.cluster.Addresses()...),
	)

	return []framework.Option{
		framework.WithProcesses(m.cluster, srv, m.daprd),
	}
}

func (m *mixedschedulers) Run(t *testing.T, ctx context.Context) {
	m.cluster.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)
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
	assert.Positive(t, m.invoked.Load())
}
