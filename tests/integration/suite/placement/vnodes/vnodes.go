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
	suite.Register(new(vnodes))
}

type vnodes struct {
	place *placement.Placement
}

func (v *vnodes) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t,
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(v.place),
	}
}

func (v *vnodes) Run(t *testing.T, ctx context.Context) {
	v.place.WaitUntilRunning(t, ctx)

	t.Run("register host without vnodes metadata (simulating daprd <1.13)", func(t *testing.T) {
		// Register the host, with API level 10 (pre v1.13)
		msg := &placementv1pb.Host{
			Name:     "myapp",
			Port:     1234,
			Entities: []string{"someactor"},
			Id:       "myapp1",
			ApiLevel: uint32(10),
		}

		// The host won't send the "dapr-accept-vnodes" metadata, simulating an older daprd version
		placementMessageCh := v.place.RegisterHostWithMetadata(t, ctx, msg, nil)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh:
				if ctx.Err() != nil {
					return
				}
				assert.Len(t, placementTables.GetEntries(), 1)
				// Check that the placement service sends the vnodes
				assert.Len(t, placementTables.GetEntries()["someactor"].GetHosts(), int(placementTables.GetReplicationFactor()))
				assert.Len(t, placementTables.GetEntries()["someactor"].GetSortedSet(), int(placementTables.GetReplicationFactor()))
			}
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("register host with vnodes metadata (simulating daprd 1.13+)", func(t *testing.T) {
		// Register the host, with API level 10 (pre v1.13)
		msg := &placementv1pb.Host{
			Name:     "myapp",
			Port:     1234,
			Entities: []string{"someactor"},
			Id:       "myapp1",
			ApiLevel: uint32(10),
		}

		// The host will send the "dapr-accept-vnodes" metadata, simulating an older daprd version
		placementMessageCh := v.place.RegisterHost(t, ctx, msg)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh:
				assert.Len(t, placementTables.GetEntries(), 1)
				// Check that the placement service doesn't send the vnodes
				assert.Empty(t, placementTables.GetEntries()["someactor"].GetHosts())
				assert.Empty(t, placementTables.GetEntries()["someactor"].GetSortedSet())
			}
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("register heterogeneous hosts (simulating daprd pre and post 1.13)", func(t *testing.T) {
		const (
			level1 = 10
			level2 = 20
		)

		msg1 := &placementv1pb.Host{
			Name:     "myapp1",
			Port:     1111,
			Entities: []string{"someactor1"},
			Id:       "myapp1",
			ApiLevel: uint32(level1),
		}
		msg2 := &placementv1pb.Host{
			Name:     "myapp2",
			Port:     2222,
			Entities: []string{"someactor2"},
			Id:       "myapp2",
			ApiLevel: uint32(level2),
		}

		// First host won't send the "dapr-accept-vnodes" metadata, simulating daprd pre 1.13
		placementMessageCh1 := v.place.RegisterHostWithMetadata(t, ctx, msg1, nil)

		// Second host will send the "dapr-accept-vnodes" metadata, simulating  daprd 1.13+
		placementMessageCh2 := v.place.RegisterHost(t, ctx, msg2)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh1:
				assert.Len(t, placementTables.GetEntries(), 2)
				// Check that the placement service sends the vnodes
				assert.Len(t, placementTables.GetEntries()["someactor1"].GetHosts(), int(placementTables.GetReplicationFactor()))
				assert.Len(t, placementTables.GetEntries()["someactor1"].GetSortedSet(), int(placementTables.GetReplicationFactor()))
				assert.Len(t, placementTables.GetEntries()["someactor2"].GetHosts(), int(placementTables.GetReplicationFactor()))
				assert.Len(t, placementTables.GetEntries()["someactor2"].GetSortedSet(), int(placementTables.GetReplicationFactor()))
			}
		}, 10*time.Second, 500*time.Millisecond)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh2:
				if ctx.Err() != nil {
					return
				}

				assert.Len(t, placementTables.GetEntries(), 2)
				// Check that the placement service doesn't send the vnodes to this (1.13+) host,
				// even though there are hosts with lower dapr versions in the cluster
				assert.Empty(t, placementTables.GetEntries()["someactor1"].GetHosts())
				assert.Empty(t, placementTables.GetEntries()["someactor1"].GetSortedSet())
				assert.Empty(t, placementTables.GetEntries()["someactor2"].GetHosts())
				assert.Empty(t, placementTables.GetEntries()["someactor2"].GetSortedSet())
			}
		}, 10*time.Second, 2000*time.Millisecond)
	})
}
