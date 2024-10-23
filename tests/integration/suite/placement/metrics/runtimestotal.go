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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(runtimestotal))
}

// runtimestotal tests placement reports API level with no maximum API level.
type runtimestotal struct {
	place  *placement.Placement
	daprdA *daprd.Daprd
	daprdB *daprd.Daprd
	daprdC *daprd.Daprd
}

func (m *runtimestotal) Setup(t *testing.T) []framework.Option {
	m.place = placement.New(t,
		placement.WithMetadataEnabled(true),
	)

	// Start two application servers in different namespaces
	srvNS1 := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype1", "myactortype2"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	srvNS2 := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype6"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	// Start three sidecars in different namespaces
	m.daprdA = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore1"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppPort(srvNS1.Port()),
		daprd.WithNamespace("ns1"))
	m.daprdB = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore1"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithNamespace("ns1"))
	m.daprdC = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore2"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppPort(srvNS2.Port()),
		daprd.WithNamespace("ns2"))

	return []framework.Option{
		framework.WithProcesses(m.place, srvNS1, srvNS2),
	}
}

func (m *runtimestotal) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)

	// No sidecars connected
	metrics := m.place.Metrics(t, ctx)
	require.Equal(t, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
	require.Equal(t, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
	require.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
	require.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))

	// Start first sidecar
	m.daprdA.Run(t, ctx)
	t.Cleanup(func() { m.daprdA.Cleanup(t) })
	m.daprdA.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)

		assert.Equal(c, 1, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdA sidecar didn't report dapr_placement_runtimes_total to Placement in time")

	// Start second sidecar
	m.daprdB.Run(t, ctx)
	t.Cleanup(func() { m.daprdB.Cleanup(t) })
	m.daprdB.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)
		assert.Equal(c, 2, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdB sidecar didn't report dapr_placement_runtimes_total to Placement in time")

	// Start third sidecar
	m.daprdC.Run(t, ctx)
	t.Cleanup(func() { m.daprdC.Cleanup(t) })
	m.daprdC.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)
		assert.Equal(c, 2, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 1, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdC sidecar didn't report dapr_placement_runtimes_total to Placement in time")

	// Stop one sidecar
	m.daprdA.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)
		assert.Equal(c, 1, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 1, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdC sidecar didn't report dapr_placement_runtimes_total to Placement in time")

	// Stop another sidecar
	m.daprdB.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)
		assert.Equal(c, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 1, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 1, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdC sidecar didn't report dapr_placement_runtimes_total to Placement in time")

	// Stop the last sidecar
	m.daprdC.Cleanup(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.place.Metrics(t, ctx)
		assert.Equal(c, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns1"]))
		assert.Equal(c, 0, int(metrics["dapr_placement_runtimes_total|host_namespace:ns2"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
		assert.Equal(t, 0, int(metrics["dapr_placement_actor_runtimes_total|host_namespace:ns2"]))
	}, 5*time.Second, 10*time.Millisecond, "daprdC sidecar didn't report dapr_placement_runtimes_total to Placement in time")
}
