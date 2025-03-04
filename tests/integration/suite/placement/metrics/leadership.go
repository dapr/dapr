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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(leadership))
}

// leadership tests placement reports API level with no maximum API level.
type leadership struct {
	fp         *ports.Ports
	placements []*placement.Placement
}

func (l *leadership) Setup(t *testing.T) []framework.Option {
	l.fp = ports.Reserve(t, 3)
	port1, port2, port3 := l.fp.Port(t), l.fp.Port(t), l.fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	l.placements = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return []framework.Option{
		framework.WithProcesses(l.fp, l.placements[0], l.placements[1], l.placements[2]),
	}
}

func (l *leadership) Run(t *testing.T, ctx context.Context) {
	// TODO @elena-kolevska Add check for correctly reflected leadership status, not just if the metric is present
	for _, p := range l.placements {
		p.WaitUntilRunning(t, ctx)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range 3 {
			metrics := l.placements[i].Metrics(c, ctx)
			leaderStatus := metrics.MatchMetric("dapr_placement_leader_status")
			raftLeaderStatus := metrics.MatchMetric("dapr_placement_raft_leader_status")
			assert.Len(c, leaderStatus, 1)
			assert.Len(c, raftLeaderStatus, 1)
		}
	}, 10*time.Second, 10*time.Millisecond)
}
