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

package reconnect

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
	suite.Register(new(basic))
}

type basic struct {
	actors []*dactors.Actors
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	actor1 := dactors.New(t,
		dactors.WithActorTypes("myactor"),
	)
	actor2 := dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPeerActor(actor1),
	)

	b.actors = []*dactors.Actors{actor1, actor2}

	return []framework.Option{
		framework.WithProcesses(actor1, actor2),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	for _, a := range b.actors {
		a.WaitUntilRunning(t, ctx)
	}

	hosts := make([]placement.Host, 0, len(b.actors))
	for _, a := range b.actors {
		hosts = append(hosts, placement.Host{
			Entities:  []string{"myactor"},
			Name:      a.Daprd().InternalGRPCAddress(),
			ID:        a.Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		})
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := b.actors[0].Placement().PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.ElementsMatch(c, hosts, table.Tables["default"].Hosts)
	}, time.Second*30, time.Millisecond*10)
}
