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

package pull

import (
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatcher_NoCapacityReparks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 1)
	h.gates[testActorType] = &gate{limit: 10}
	h.report(1, 0)

	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	require.Len(t, h.dispatched[0], 1)
	require.Equal(t, uint32(1), h.gates[testActorType].current)

	// Another scheduler filled the sidecar's last slot first.
	req.Repark()
	h.pump(t)

	assert.NotContains(t, h.results, req, "a refused trigger is not resolved to the cron")
	assert.Equal(t, uint32(0), h.pool(0).credit, "the refusing sidecar is full until it reports otherwise")
	assert.Equal(t, uint32(0), h.gates[testActorType].current, "the shared gate is released")
	assert.Equal(t, 1, h.d.pending[testActorType].len())
	assert.Empty(t, h.d.sidecars[sidecarOf(0)].flights)

	// The hooks of the refused request are spent.
	req.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.NotContains(t, h.results, req)

	h.report(1, 1)
	require.Len(t, h.dispatched[1], 1, "the parked trigger goes to the sidecar that reports a slot")
	again := h.dispatched[1][0]
	assert.Same(t, req.Job, again.Job)
	again.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, req), "the cron callback travels with the requeued trigger")
}
