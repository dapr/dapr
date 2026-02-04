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
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Cluster struct {
	places []*placement.Placement
	fp     *ports.Ports
}

func New(t *testing.T, fopts ...Option) *Cluster {
	t.Helper()

	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
	}
	places := []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	return &Cluster{
		fp:     fp,
		places: places,
	}
}

func (c *Cluster) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	c.fp.Free(t)
	for _, p := range c.places {
		p.Run(t, ctx)
	}
}

func (c *Cluster) Cleanup(t *testing.T) {
	t.Helper()

	var wg sync.WaitGroup
	wg.Add(len(c.places))
	for _, s := range c.places {
		go func(p *placement.Placement) {
			p.Cleanup(t)
			wg.Done()
		}(s)
	}
	wg.Wait()
}

func (c *Cluster) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, p := range c.places {
		p.WaitUntilRunning(t, ctx)
	}
}

func (c *Cluster) Addresses() []string {
	addrs := make([]string, 0, len(c.places))
	for _, p := range c.places {
		addrs = append(addrs, p.Address())
	}

	return addrs
}

func (c *Cluster) Leader(t *testing.T, ctx context.Context) *placement.Placement {
	t.Helper()
	for {
		for _, p := range c.places {
			if m := p.Metrics(t, ctx).MatchMetric("dapr_placement_leader_status"); len(m) == 1 && m[0].Value == 1 {
				return p
			}
		}

		select {
		case <-ctx.Done():
			require.Fail(t, "timed out waiting for leader")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
