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

func (n *noMax) Run(t *testing.T, parentCtx context.Context) {
	httpClient := util.HTTPClient(t)

	ctx, cancel := context.WithCancel(parentCtx)
	t.Cleanup(cancel)

	n.place.WaitUntilRunning(t, ctx)

	// Connect
	conn, err := establishConn(ctx, n.place.Port())
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
				case uint32:
					old := currentVersion.Swap(msg)
					if old != msg {
						lastVersionUpdate.Store(time.Now().Unix())
					}
				}
			}
		}
	}()

	// Register the first host with API level 10
	stopCh1 := make(chan struct{})
	registerHost(t, ctx, conn, "myapp1", 10, placementMessageCh, stopCh1)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(10), currentVersion.Load())
	}, 10*time.Second, 50*time.Millisecond)
	lastUpdate := lastVersionUpdate.Load()

	var tableVersion int
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		tableVersion = checkAPILevelInState(t, httpClient, n.place.HealthzPort(), 10)
	}, 5*time.Second, 100*time.Millisecond)

	// Register the second host with API level 20
	stopCh2 := make(chan struct{})
	registerHost(t, ctx, conn, "myapp2", 20, placementMessageCh, stopCh2)

	// After 3s, we should not receive an update
	// This can take a while as dissemination happens on intervals
	time.Sleep(3 * time.Second)
	require.Equal(t, lastUpdate, lastVersionUpdate.Load())

	// API level should still be 10 (but table version should have increased)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		newTableVersion := checkAPILevelInState(t, httpClient, n.place.HealthzPort(), 10)
		assert.Greater(t, newTableVersion, tableVersion)
	}, 10*time.Second, 100*time.Millisecond)

	// Stop the first host, and the in API level should increase
	close(stopCh1)
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(20), currentVersion.Load())
	}, 10*time.Second, 50*time.Millisecond)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		tableVersion = checkAPILevelInState(t, httpClient, n.place.HealthzPort(), 20)
	}, 5*time.Second, 100*time.Millisecond)

	// Trying to register a host with version 5 should fail
	registerHostFailing(t, ctx, conn, 5)

	// Stop the second host too
	close(stopCh2)

	// Ensure that the table version increases, but the API level remains the same
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		newTableVersion := checkAPILevelInState(t, httpClient, n.place.HealthzPort(), 20)
		assert.Greater(t, newTableVersion, tableVersion)
	}, 5*time.Second, 100*time.Millisecond)

	// Trying to register a host with version 10 should fail
	registerHostFailing(t, ctx, conn, 10)
}
