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

package config

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
	suite.Register(new(ha))
}

// ha asserts every replica of a placement cluster serves the Config RPC,
// leader or not, since the dissemination timeout is static configuration.
type ha struct {
	places []*placement.Placement
}

func (h *ha) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
		placement.WithDisseminateTimeout(time.Second * 9),
	}
	h.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return []framework.Option{
		framework.WithProcesses(fp, h.places[0], h.places[1], h.places[2]),
	}
}

func (h *ha) Run(t *testing.T, ctx context.Context) {
	for _, p := range h.places {
		p.WaitUntilRunning(t, ctx)
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var leaders int
		for _, p := range h.places {
			if p.IsLeader(t, ctx) {
				leaders++
			}
		}
		assert.Equal(c, 1, leaders, "expected exactly one leader")
	}, time.Second*20, time.Millisecond*10)

	for _, p := range h.places {
		resp, err := p.Client(t, ctx).Config(ctx, new(v1pb.ConfigRequest))
		require.NoError(t, err)
		require.NotNil(t, resp.GetDisseminateTimeout())
		assert.Equal(t, time.Second*9, resp.GetDisseminateTimeout().AsDuration())
	}
}
