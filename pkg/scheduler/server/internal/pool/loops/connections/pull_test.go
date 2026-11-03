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

package connections

import (
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// The pull dispatch logic is tested in the pull package. These tests cover
// how connections wires it into trigger routing.

const pullActorType = "dapr.internal.default.myapp.activity"

func pullTrigger(actorID, name string) *loops.TriggerRequest {
	req := makeTriggerRequest(pullActorType, actorID, name)
	req.Job.Name = "run-activity"
	req.Job.Metadata.Pull = new(true)
	req.Job.Metadata.Namespace = "default"
	req.Job.Metadata.AppId = "myapp"
	req.ResultFn = func(api.TriggerResponseResult) {}
	return req
}

func TestPullDispatch_FallsBackToRegularRouting(t *testing.T) {
	t.Parallel()

	var dispatched []loops.EventStream
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		dispatched = append(dispatched, e)
	})
	c := connsCache.New().(*connections)
	c.schedulerCount = 1
	c.pull = c.newPull()
	c.streamPool.Add(store.Options{Loop: streamLoop, ActorTypes: []string{pullActorType}})

	req := pullTrigger("wf::1::0", "act")
	c.handleTriggerRequest(req)

	require.Len(t, dispatched, 1, "a pull trigger with no sidecar reporting slots uses the regular actor routing")
	assert.Same(t, req, dispatched[0].(*loops.TriggerRequest))
}
