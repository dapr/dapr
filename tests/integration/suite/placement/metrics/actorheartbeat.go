/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorheartbeat))
}

// actorheartbeat tests placement reports API level with no maximum API level.
type actorheartbeat struct {
	place *placement.Placement
	daprd *daprd.Daprd
}

func (m *actorheartbeat) Setup(t *testing.T) []framework.Option {
	m.place = placement.New(t,
		placement.WithMetadataEnabled(true),
	)

	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype1", "myactortype2"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore1"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithNamespace("ns1"))

	return []framework.Option{
		framework.WithProcesses(m.place, m.daprd, srv),
	}
}

func (m *actorheartbeat) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	label1 := fmt.Sprintf("dapr_placement_actor_heartbeat_timestamp|actor_type:%s|app_id:%s|host_name:%s|host_namespace:ns1|pod_name:", "myactortype1", m.daprd.AppID(), m.daprd.InternalGRPCAddress())
	label2 := fmt.Sprintf("dapr_placement_actor_heartbeat_timestamp|actor_type:%s|app_id:%s|host_name:%s|host_namespace:ns1|pod_name:", "myactortype2", m.daprd.AppID(), m.daprd.InternalGRPCAddress())

	var m1, m2, i int
	// Repeat the cycle 3 times to check if the actor heartbeat is increasing
	// We're using `require` because the condition should be true every time
	// The repeat cycle is a bit over 1sec so that we're sure we're not catching some edge condition
	require.Eventually(t, func() bool {
		i++
		metrics := m.place.Metrics(t, ctx)

		require.Greater(t, int(metrics[label1]), m1)
		require.Greater(t, int(metrics[label2]), m2)
		m1 = int(metrics[label1])
		m2 = int(metrics[label2])
		return i >= 2
	}, 10*time.Second, 1100*time.Millisecond)
}
