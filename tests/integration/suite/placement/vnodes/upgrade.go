/*
Copyright 2024 The Dapr Authors
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

package vnodes

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

	// Register the first host, with a lower API level (pre v1.13)
	msg1 := &placementv1pb.Host{
		Name:     "myapp1",
		Port:     1111,
		Entities: []string{"someactor1"},
		Id:       "myapp1",
		ApiLevel: uint32(level1),
	}
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	placementMessageCh1 := v.place.RegisterHost(t, ctx1, msg1)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh1:
			if ctx.Err() != nil {
				return
			}
			assert.Equal(t, uint32(level1), placementTables.GetApiLevel())
			assert.Len(t, placementTables.GetEntries(), 1)
			// Check that the placement service sends the vnodes, because of the older cluster API level
			assert.Len(t, placementTables.GetEntries()["someactor1"].GetHosts(), int(placementTables.GetReplicationFactor()))
			assert.Len(t, placementTables.GetEntries()["someactor1"].GetSortedSet(), int(placementTables.GetReplicationFactor()))
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Register the second host, with a higher API level (v1.13+)
	msg2 := &placementv1pb.Host{
		Name:     "myapp2",
		Port:     2222,
		Entities: []string{"someactor2"},
		Id:       "myapp2",
		ApiLevel: uint32(level2),
	}
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	placementMessageCh2 := v.place.RegisterHost(t, ctx2, msg2)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh2:
			if ctx.Err() != nil {
				return
			}

			assert.Equal(t, uint32(level1), placementTables.GetApiLevel())
			assert.Len(t, placementTables.GetEntries(), 2)
			// Check that the placement service still sends the vnodes, because cluster level is still pre v1.13
			assert.Len(t, placementTables.GetEntries()["someactor2"].GetHosts(), int(placementTables.GetReplicationFactor()))
			assert.Len(t, placementTables.GetEntries()["someactor2"].GetSortedSet(), int(placementTables.GetReplicationFactor()))
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Stop the first host; the API level should increase and vnodes shouldn't be sent anymore
	cancel1()

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case placementTables := <-placementMessageCh2:
			if ctx.Err() != nil {
				return
			}

			assert.Equal(t, uint32(level2), placementTables.GetApiLevel())
			assert.Len(t, placementTables.GetEntries(), 1)
			// Check that the vnodes are not sent anymore, because the minimum API level of the cluster is 20+
			assert.Empty(t, placementTables.GetEntries()["someactor2"].GetHosts())
			assert.Empty(t, placementTables.GetEntries()["someactor2"].GetSortedSet())
		}
	}, 10*time.Second, 100*time.Millisecond)
}
