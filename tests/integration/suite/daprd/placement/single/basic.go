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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.place = placement.New(t)
	scheduler := scheduler.New(t)

	b.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(b.place, scheduler, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	table := b.place.PlacementTables(t, ctx)
	assert.Equal(t, &placement.TableState{
		Tables: map[string]*placement.Table{
			"default": {
				Version: 1,
				Hosts: []placement.Host{
					{
						Name:      b.daprd.InternalGRPCAddress(),
						ID:        b.daprd.AppID(),
						APIVLevel: 20,
						Namespace: "default",
					},
				},
			},
		},
	}, table)
}
