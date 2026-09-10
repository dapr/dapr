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

package notypes

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invoke))
}

type invoke struct {
	noTypes   *actors.Actors
	withTypes *actors.Actors

	called atomic.Int64
}

func (i *invoke) Setup(t *testing.T) []framework.Option {
	i.noTypes = actors.New(t)

	i.withTypes = actors.New(t,
		actors.WithPeerActor(i.noTypes),
		actors.WithActorTypes("mytype"),
		actors.WithActorTypeHandler("mytype", func(nethttp.ResponseWriter, *nethttp.Request) {
			i.called.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(i.noTypes, i.withTypes),
	}
}

func (i *invoke) Run(t *testing.T, ctx context.Context) {
	i.noTypes.WaitUntilRunning(t, ctx)
	i.withTypes.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := i.noTypes.Placement().PlacementTables(t, ctx)
		if !assert.Contains(c, table.Tables, "default") {
			return
		}
		assert.Equal(c, []placement.Host{
			{
				Entities:  []string{"mytype"},
				Name:      i.withTypes.Daprd().InternalGRPCAddress(),
				ID:        i.withTypes.Daprd().AppID(),
				APIVLevel: 20,
				Namespace: "default",
			},
		}, table.Tables["default"].Hosts)
	}, time.Second*10, time.Millisecond*10)

	client := i.noTypes.GRPCClient(t, ctx)
	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "mytype",
		ActorId:   "1",
		Method:    "foo",
	})
	require.NoError(t, err)

	assert.Positive(t, i.called.Load())
}
