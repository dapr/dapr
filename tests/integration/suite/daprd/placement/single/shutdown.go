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
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(shutdown))
}

type shutdown struct {
	actors *dactors.Actors
}

func (s *shutdown) Setup(t *testing.T) []framework.Option {
	s.actors = dactors.New(t,
		dactors.WithActorTypes("mytype"),
	)

	return []framework.Option{
		framework.WithProcesses(s.actors),
	}
}

func (s *shutdown) Run(t *testing.T, ctx context.Context) {
	s.actors.WaitUntilRunning(t, ctx)

	table := s.actors.Placement().PlacementTables(t, ctx)
	assert.Equal(t, &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Entities:  []string{"mytype"},
						Name:      s.actors.Daprd().InternalGRPCAddress(),
						ID:        s.actors.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}, table)

	s.actors.Daprd().Cleanup(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.actors.Placement().PlacementTables(t, ctx)
		assert.Equal(c, &placement.TableState{
			Tables: make(map[string]*placement.Table),
		}, table)
	}, time.Second*10, time.Millisecond*10)
}
