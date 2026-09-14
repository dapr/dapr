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

package cluster

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement/cluster"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(follower))
}

// follower gives a sidecar only the follower replicas of the placement
// service, which refuse every stream with FailedPrecondition on the first
// Recv. The reconnect must back off rather than spin.
type follower struct {
	place *cluster.Cluster
	sched *scheduler.Scheduler
}

func (f *follower) Setup(t *testing.T) []framework.Option {
	f.place = cluster.New(t)
	f.sched = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(f.place, f.sched),
	}
}

func (f *follower) Run(t *testing.T, ctx context.Context) {
	f.place.WaitUntilRunning(t, ctx)
	f.sched.WaitUntilRunning(t, ctx)

	leader := f.place.Leader(t, ctx)
	followers := make([]string, 0, 2)
	for _, addr := range f.place.Addresses() {
		if addr != leader.Address() {
			followers = append(followers, addr)
		}
	}
	require.Len(t, followers, 2)

	lines := logline.New(t, logline.WithCaptureAll())
	lines.Run(t, ctx)
	t.Cleanup(func() { lines.Cleanup(t) })

	d := daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(f.sched),
		daprd.WithPlacementAddresses(followers...),
		daprd.WithLogLineStdout(lines),
	)
	d.Run(t, ctx)
	t.Cleanup(func() { d.Cleanup(t) })

	closes := func() int {
		return bytes.Count(lines.StdoutBuffer(), []byte("Placement stream closed"))
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, closes())
	}, time.Second*20, time.Millisecond*10)

	// The half second backoff allows at most ~12 closes in 3 seconds.
	// Without it the reconnect loop produces thousands.
	before := closes()
	time.Sleep(time.Second * 3)
	during := closes() - before
	assert.Less(t, during, 30,
		"a refused placement stream must back off, not spin: %d closes in 3s", during)
}
