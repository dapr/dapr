/*
Copyright 2023 The Dapr Authors
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

package quorum

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notls))
}

// notls tests placement can find quorum with tls disabled.
type notls struct {
	places []*placement.Placement
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	n.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return []framework.Option{
		framework.WithProcesses(fp, n.places[0], n.places[1], n.places[2]),
	}
}

func (n *notls) Run(t *testing.T, ctx context.Context) {
	n.places[0].WaitUntilRunning(t, ctx)
	n.places[1].WaitUntilRunning(t, ctx)
	n.places[2].WaitUntilRunning(t, ctx)

	var leader *placement.Placement
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, p := range []*placement.Placement{n.places[0], n.places[1], n.places[2]} {
			if p.IsLeader(t, ctx) {
				leader = p
				break
			}
		}
		assert.NotNil(c, leader, "no leader found")
	}, time.Second*20, time.Millisecond*10)

	require.NotNil(t, leader, "no leader found")

	client := leader.Client(t, ctx)
	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	for range 3 {
		err := stream.Send(&v1pb.Host{
			Name:      "app-1",
			Port:      1234,
			Load:      1,
			Entities:  []string{"entity-1", "entity-2"},
			Id:        "app-1",
			Pod:       "pod-1",
			Namespace: "default",
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		o, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(c, "update", o.GetOperation())
		if assert.NotNil(c, o.GetTables()) {
			assert.Len(c, o.GetTables().GetEntries(), 2)
			assert.Contains(c, o.GetTables().GetEntries(), "entity-1")
			assert.Contains(c, o.GetTables().GetEntries(), "entity-2")
		}
	}, time.Second*20, time.Millisecond*10)
}
