/*
Copyright 2021 The Dapr Authors
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

package table

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(vNodesRollingUpgrade))
}

type vNodesRollingUpgrade struct {
	place *placement.Placement
}

func (v *vNodesRollingUpgrade) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t,
		placement.WithLogLevel("debug"),
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(v.place),
	}
}

func (v *vNodesRollingUpgrade) Run(t *testing.T, ctx context.Context) {
	const (
		level1 = 10
		level2 = 20
	)

	v.place.WaitUntilRunning(t, ctx)

	// Connect
	conn1, err := v.place.EstablishConn(ctx)
	require.NoError(t, err)
	conn2, err := v.place.EstablishConn(ctx)
	require.NoError(t, err)

	// Collect messages
	placementMessageCh := make(chan any)

	// Register the first host, with a lower API level (pre v1.13)
	stopCh1 := make(chan struct{})
	msg1 := &placementv1pb.Host{
		Name:     "myapp1",
		Port:     1111,
		Entities: []string{"someactor1"},
		Id:       "myapp1",
		ApiLevel: uint32(level1),
	}
	placement.RegisterHost(t, ctx, conn1, msg1, placementMessageCh, stopCh1)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case msgAny := <-placementMessageCh:
			if ctx.Err() != nil {
				return
			}
			switch msg := msgAny.(type) {
			case error:
				assert.Fail(t, "Received an error in the placement channel: '%v'", msg)
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level1), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				// Check that the placement service sends the vnodes, because of the older cluster API level
				assert.Len(t, msg.GetEntries()["someactor1"].GetHosts(), int(msg.GetReplicationFactor()))
				assert.Len(t, msg.GetEntries()["someactor1"].GetSortedSet(), int(msg.GetReplicationFactor()))
			}
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Register the second host, with a higher API level (v1.13+)
	stopCh2 := make(chan struct{})
	msg2 := &placementv1pb.Host{
		Name:     "myapp2",
		Port:     2222,
		Entities: []string{"someactor2"},
		Id:       "myapp2",
		ApiLevel: uint32(level2),
	}
	placement.RegisterHost(t, ctx, conn2, msg2, placementMessageCh, stopCh2)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case msgAny := <-placementMessageCh:
			if ctx.Err() != nil {
				return
			}
			switch msg := msgAny.(type) {
			case error:
				assert.Fail(t, "Received an error in the placement channel: '%v'", msg)
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level1), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 2)
				// Check that the placement service still sends the vnodes, because cluster level is still pre v1.13
				assert.Len(t, msg.GetEntries()["someactor2"].GetHosts(), int(msg.GetReplicationFactor()))
				assert.Len(t, msg.GetEntries()["someactor2"].GetSortedSet(), int(msg.GetReplicationFactor()))
			}
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Stop the first host; the API level should increase and vnodes shouldn't be sent anymore
	close(stopCh1)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case msgAny := <-placementMessageCh:
			if ctx.Err() != nil {
				return
			}
			switch msg := msgAny.(type) {
			case error:
				assert.Fail(t, "Received an error in the placement channel: '%v'", msg)
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level2), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				// Check that the vnodes are not sent anymore, because the minimum API level of the cluster is 20+
				assert.Empty(t, msg.GetEntries()["someactor2"].GetHosts())
				assert.Empty(t, msg.GetEntries()["someactor2"].GetSortedSet())
			}
		}
	}, 10*time.Second, 100*time.Millisecond)
}
