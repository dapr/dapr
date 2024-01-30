/*
Copyright 2023 The Dapr Authors
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

package apilevel

import (
	"context"
	"log"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noMax))
}

// noMax tests placement reports API level with no maximum API level.
type noMax struct {
	place *placement.Placement
}

func (n *noMax) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t,
		placement.WithLogLevel("debug"),
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *noMax) Run(t *testing.T, ctx context.Context) {
	const level1 = 20
	const level2 = 30

	httpClient := util.HTTPClient(t)

	n.place.WaitUntilRunning(t, ctx)

	// Connect
	conn, err := n.place.EstablishConn(ctx)
	require.NoError(t, err)

	// Collect messages
	placementMessageCh := make(chan any)
	currentVersion := atomic.Uint32{}
	lastVersionUpdate := atomic.Int64{}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msgAny := <-placementMessageCh:
				if ctx.Err() != nil {
					return
				}
				switch msg := msgAny.(type) {
				case error:
					log.Printf("Received an error in the channel: '%v'", msg)
					return
				case *placementv1pb.PlacementTables:
					newAPILevel := msg.GetApiLevel()
					oldAPILevel := currentVersion.Swap(newAPILevel)
					if oldAPILevel != newAPILevel {
						lastVersionUpdate.Store(time.Now().Unix())
					}
				}
			}
		}
	}()

	// Register the first host with the lower API level
	stopCh1 := make(chan struct{})
	msg1 := &placementv1pb.Host{
		Name:     "myapp1",
		Port:     1111,
		Entities: []string{"someactor1"},
		Id:       "myapp1",
		ApiLevel: uint32(level1),
	}
	placement.RegisterHost(t, ctx, conn, msg1, placementMessageCh, stopCh1)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(level1), currentVersion.Load())
	}, 10*time.Second, 50*time.Millisecond)
	lastUpdate := lastVersionUpdate.Load()

	var tableVersion int
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		tableVersion = placement.CheckAPILevelInState(t, httpClient, n.place.HealthzPort(), level1)
	}, 5*time.Second, 100*time.Millisecond)

	// Register the second host with the higher API level
	stopCh2 := make(chan struct{})
	msg2 := &placementv1pb.Host{
		Name:     "myapp2",
		Port:     2222,
		Entities: []string{"someactor2"},
		Id:       "myapp2",
		ApiLevel: uint32(level2),
	}
	placement.RegisterHost(t, ctx, conn, msg2, placementMessageCh, stopCh2)

	// After 3s, we should not receive an update
	// This can take a while as dissemination happens on intervals
	time.Sleep(3 * time.Second)
	require.Equal(t, lastUpdate, lastVersionUpdate.Load())

	// API level should still be lower (20), but table version should have increased
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		newTableVersion := placement.CheckAPILevelInState(t, httpClient, n.place.HealthzPort(), level1)
		assert.Greater(t, newTableVersion, tableVersion)
	}, 10*time.Second, 100*time.Millisecond)

	// Stop the first host, and the in API level should increase
	close(stopCh1)
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(level2), currentVersion.Load())
	}, 10*time.Second, 50*time.Millisecond)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		tableVersion = placement.CheckAPILevelInState(t, httpClient, n.place.HealthzPort(), level2)
	}, 5*time.Second, 100*time.Millisecond)

	// Trying to register a host with version 5 should fail
	placement.RegisterHostFailing(t, ctx, conn, 5)

	// Stop the second host too
	close(stopCh2)

	// Ensure that the table version increases, but the API level remains the same
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		newTableVersion := placement.CheckAPILevelInState(t, httpClient, n.place.HealthzPort(), level2)
		assert.Greater(t, newTableVersion, tableVersion)
	}, 5*time.Second, 100*time.Millisecond)

	// Trying to register a host with version 10 should fail
	placement.RegisterHostFailing(t, ctx, conn, level1)
}
