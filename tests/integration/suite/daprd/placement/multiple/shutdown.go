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

package multiple

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(shutdown))
}

type shutdown struct {
	actors []*dactors.Actors
}

func (s *shutdown) Setup(t *testing.T) []framework.Option {
	actor1 := dactors.New(t,
		dactors.WithActorTypes("mytype"),
	)
	actor2 := dactors.New(t,
		dactors.WithActorTypes("mytype"),
		dactors.WithPeerActor(actor1),
	)
	actor3 := dactors.New(t,
		dactors.WithActorTypes("mytype"),
		dactors.WithPeerActor(actor1),
	)

	s.actors = []*dactors.Actors{actor1, actor2, actor3}

	return []framework.Option{
		framework.WithProcesses(actor1, actor2, actor3),
	}
}

func (s *shutdown) Run(t *testing.T, ctx context.Context) {
	for _, a := range s.actors {
		a.WaitUntilRunning(t, ctx)
	}

	hosts := make([]placement.Host, 0, len(s.actors))
	for _, a := range s.actors {
		hosts = append(hosts, placement.Host{
			Entities:  []string{"mytype"},
			Name:      a.Daprd().InternalGRPCAddress(),
			ID:        a.Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		})
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.actors[0].Placement().PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.ElementsMatch(c, hosts, table.Tables["default"].Hosts)
		assert.Positive(c, table.Tables["default"].Version)
	}, time.Second*10, time.Millisecond*10)

	for i := range s.actors[:2] {
		s.actors[i].Daprd().Cleanup(t)
		hosts = hosts[1:]
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			table := s.actors[0].Placement().PlacementTables(t, ctx)
			if !assert.NotNil(c, table.Tables["default"]) {
				return
			}
			assert.ElementsMatch(c, hosts, table.Tables["default"].Hosts)
		}, time.Second*20, time.Millisecond*10)
	}

	s.actors[2].Daprd().Cleanup(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.actors[0].Placement().PlacementTables(t, ctx)
		assert.Equal(c, &placement.TableState{
			Tables: make(map[string]*placement.Table),
		}, table)
	}, time.Second*10, time.Millisecond*10)
}
