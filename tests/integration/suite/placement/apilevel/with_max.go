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
	suite.Register(new(withMax))
}

// withMax tests placement reports API level with maximum API level.
type withMax struct {
	place *placement.Placement
}

func (n *withMax) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t,
		placement.WithLogLevel("debug"),
		placement.WithMaxAPILevel(25),
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *withMax) Run(t *testing.T, parentCtx context.Context) {
	const level1 = 20
	const level2 = 30

	httpClient := util.HTTPClient(t)

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

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

	// Register the first host with the lower API level
	stopCh1 := make(chan struct{})
	registerHost(t, ctx, conn, "myapp1", level1, placementMessageCh, stopCh1)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(level1), currentVersion.Load())
	}, 10*time.Second, 50*time.Millisecond)
	lastUpdate := lastVersionUpdate.Load()

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		checkAPILevelInState(t, httpClient, n.place.HealthzPort(), level1)
	}, 5*time.Second, 100*time.Millisecond)

	// Register the second host with API level 20
	registerHost(t, ctx, conn, "myapp2", level2, placementMessageCh, nil)

	// After 3s, we should not receive an update
	// This can take a while as dissemination happens on intervals
	time.Sleep(3 * time.Second)
	require.Equal(t, lastUpdate, lastVersionUpdate.Load())

	// API level should not increase
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		checkAPILevelInState(t, httpClient, n.place.HealthzPort(), level1)
	}, 5*time.Second, 100*time.Millisecond)

	// Stop the first host, and the in API level should increase to the max (25)
	close(stopCh1)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(25), currentVersion.Load())
	}, 15*time.Second, 50*time.Millisecond)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		checkAPILevelInState(t, httpClient, n.place.HealthzPort(), 25)
	}, 5*time.Second, 100*time.Millisecond)
}
