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

package single

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(shutdown))
}

type shutdown struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (s *shutdown) Setup(t *testing.T) []framework.Option {
	s.place = placement.New(t)
	scheduler := scheduler.New(t)

	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, scheduler, s.daprd),
	}
}

func (s *shutdown) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	table := s.place.PlacementTables(t, ctx)
	assert.Equal(t, &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Name:      s.daprd.InternalGRPCAddress(),
						ID:        s.daprd.AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}, table)

	s.daprd.Cleanup(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		assert.Equal(t, &placement.TableState{
			Tables: map[string]*placement.Table{},
		}, table)
	}, time.Second*10, time.Millisecond*10)
}
