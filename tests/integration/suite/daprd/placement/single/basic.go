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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	actors *dactors.Actors
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.actors = dactors.New(t,
		dactors.WithActorTypes("mytype"),
	)

	return []framework.Option{
		framework.WithProcesses(b.actors),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.actors.WaitUntilRunning(t, ctx)

	table := b.actors.Placement().PlacementTables(t, ctx)
	assert.Equal(t, &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Entities:  []string{"mytype"},
						Name:      b.actors.Daprd().InternalGRPCAddress(),
						ID:        b.actors.Daprd().AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}, table)
}
