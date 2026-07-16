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
	suite.Register(new(lateJoin))
}

type lateJoin struct {
	withTypes *actors.Actors
	noTypes   *actors.Actors

	called atomic.Int64
}

func (l *lateJoin) Setup(t *testing.T) []framework.Option {
	l.withTypes = actors.New(t,
		actors.WithActorTypes("mytype"),
		actors.WithActorTypeHandler("mytype", func(nethttp.ResponseWriter, *nethttp.Request) {
			l.called.Add(1)
		}),
	)

	l.noTypes = actors.New(t,
		actors.WithPeerActor(l.withTypes),
	)

	// Only start the withTypes daprd initially.
	return []framework.Option{
		framework.WithProcesses(l.withTypes),
	}
}

func (l *lateJoin) Run(t *testing.T, ctx context.Context) {
	l.withTypes.WaitUntilRunning(t, ctx)

	// Verify placement table has the typed host.
	expHosts := []placement.Host{
		{
			Entities:  []string{"mytype"},
			Name:      l.withTypes.Daprd().InternalGRPCAddress(),
			ID:        l.withTypes.Daprd().AppID(),
			APIVLevel: 20,
			Namespace: "default",
		},
	}
	table := l.withTypes.Placement().PlacementTables(t, ctx)
	assert.Equal(t, expHosts, table.Tables["default"].Hosts)

	// Now start the no-types daprd.
	l.noTypes.Run(t, ctx)
	t.Cleanup(func() { l.noTypes.Cleanup(t) })
	l.noTypes.WaitUntilRunning(t, ctx)

	// Wait for the no-types daprd to settle, then record the version.
	time.Sleep(time.Millisecond * 500)
	table = l.withTypes.Placement().PlacementTables(t, ctx)
	assert.Equal(t, expHosts, table.Tables["default"].Hosts)
	versionAfterJoin := table.Tables["default"].Version

	// The no-types daprd should be able to invoke actors on the withTypes daprd.
	client := l.noTypes.GRPCClient(t, ctx)
	_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "mytype",
		ActorId:   "1",
		Method:    "foo",
	})
	require.NoError(t, err)

	assert.Positive(t, l.called.Load())

	// Kill the no-types daprd. The version should remain unchanged since it had
	// no entities and its disconnection should not trigger dissemination.
	l.noTypes.Daprd().Cleanup(t)
	time.Sleep(time.Millisecond * 500)
	table = l.withTypes.Placement().PlacementTables(t, ctx)
	assert.Equal(t, expHosts, table.Tables["default"].Hosts)
	assert.Equal(t, versionAfterJoin, table.Tables["default"].Version)
}
