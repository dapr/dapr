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

package dissemination

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notls))
}

type notls struct {
	place *placement.Placement
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *notls) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	t.Run("actors in different namespaces are disseminated properly", func(t *testing.T) {
		host1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1", "actor10"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		host2 := &v1pb.Host{
			Name:      "myapp2",
			Namespace: "ns2",
			Port:      1232,
			Entities:  []string{"actor2", "actor3"},
			Id:        "myapp2",
			ApiLevel:  uint32(20),
		}
		host3 := &v1pb.Host{
			Name:      "myapp3",
			Namespace: "ns2",
			Port:      1233,
			Entities:  []string{"actor4", "actor5", "actor6", "actor10"},
			Id:        "myapp3",
			ApiLevel:  uint32(20),
		}

		ctx3, cancel3 := context.WithCancel(ctx)
		placementMessageCh1 := n.place.RegisterHost(t, ctx, host1)
		placementMessageCh2 := n.place.RegisterHost(t, ctx, host2)
		placementMessageCh3 := n.place.RegisterHost(t, ctx3, host3)

		select {
		case <-ctx.Done():
			cancel3()
			return
		case placementTables := <-placementMessageCh1:
			require.Len(t, placementTables.GetEntries(), 2)
			require.Contains(t, placementTables.GetEntries(), "actor1")
			require.Contains(t, placementTables.GetEntries(), "actor10")

			entry, ok := placementTables.GetEntries()["actor10"]
			require.True(t, ok)
			loadMap := entry.GetLoadMap()
			require.Len(t, loadMap, 1)
			require.Contains(t, loadMap, host1.GetName())
		}

		// Dissemination is done properly on host 2
		msgCnt := 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh2:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(c, placementTables.GetEntries(), 6)
				assert.Contains(c, placementTables.GetEntries(), "actor2")
				assert.Contains(c, placementTables.GetEntries(), "actor3")
				assert.Contains(c, placementTables.GetEntries(), "actor4")
				assert.Contains(c, placementTables.GetEntries(), "actor5")
				assert.Contains(c, placementTables.GetEntries(), "actor6")
				assert.Contains(c, placementTables.GetEntries(), "actor10")

				entry, ok := placementTables.GetEntries()["actor10"]
				if assert.True(c, ok) {
					loadMap := entry.GetLoadMap()
					assert.Len(c, loadMap, 1)
					assert.Contains(c, loadMap, host3.GetName())
				}
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)

		// Dissemination is done properly on host 3
		msgCnt = 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh3:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(c, placementTables.GetEntries(), 6)
				assert.Contains(c, placementTables.GetEntries(), "actor2")
				assert.Contains(c, placementTables.GetEntries(), "actor3")
				assert.Contains(c, placementTables.GetEntries(), "actor4")
				assert.Contains(c, placementTables.GetEntries(), "actor5")
				assert.Contains(c, placementTables.GetEntries(), "actor6")
				assert.Contains(c, placementTables.GetEntries(), "actor10")

				entry, ok := placementTables.GetEntries()["actor10"]
				if assert.True(c, ok) {
					loadMap := entry.GetLoadMap()
					assert.Len(c, loadMap, 1)
					assert.Contains(c, loadMap, host3.GetName())
				}
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)

		cancel3() // Disconnect host 3

		// Host 2
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh2:
				if ctx.Err() != nil {
					return
				}

				assert.Len(t, placementTables.GetEntries(), 2)
				assert.Contains(t, placementTables.GetEntries(), "actor2")
				assert.Contains(t, placementTables.GetEntries(), "actor3")
			}
		}, 10*time.Second, 10*time.Millisecond)
	})

	// old sidecars = pre 1.14
	t.Run("namespaces are disseminated properly when there are old sidecars in the cluster", func(t *testing.T) {
		host1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		host2 := &v1pb.Host{
			Name:     "myapp2",
			Port:     1232,
			Entities: []string{"actor2", "actor3"},
			Id:       "myapp2",
			ApiLevel: uint32(20),
		}
		host3 := &v1pb.Host{
			Name:     "myapp3",
			Port:     1233,
			Entities: []string{"actor4", "actor5", "actor6"},
			Id:       "myapp3",
			ApiLevel: uint32(20),
		}

		ctx3, cancel3 := context.WithCancel(ctx)
		placementMessageCh1 := n.place.RegisterHost(t, ctx, host1)
		placementMessageCh2 := n.place.RegisterHost(t, ctx, host2)
		placementMessageCh3 := n.place.RegisterHost(t, ctx3, host3)

		// Dissemination is done properly on host 1
		msgCnt := 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh1:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(t, placementTables.GetEntries(), 1)
				assert.Contains(t, placementTables.GetEntries(), "actor1")
			}

			// There's only one host in ns1, so we'll receive only one message
			assert.Equal(c, 1, msgCnt)
		}, 10*time.Second, 10*time.Millisecond)

		// Dissemination is done properly on host 2
		msgCnt = 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh2:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(c, placementTables.GetEntries(), 5)
				assert.Contains(c, placementTables.GetEntries(), "actor2")
				assert.Contains(c, placementTables.GetEntries(), "actor3")
				assert.Contains(c, placementTables.GetEntries(), "actor4")
				assert.Contains(c, placementTables.GetEntries(), "actor5")
				assert.Contains(c, placementTables.GetEntries(), "actor6")
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)

		// Dissemination is done properly on host 3
		msgCnt = 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh3:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(c, placementTables.GetEntries(), 5)
				assert.Contains(c, placementTables.GetEntries(), "actor2")
				assert.Contains(c, placementTables.GetEntries(), "actor3")
				assert.Contains(c, placementTables.GetEntries(), "actor4")
				assert.Contains(c, placementTables.GetEntries(), "actor5")
				assert.Contains(c, placementTables.GetEntries(), "actor6")
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)

		cancel3() // Disconnect host 3

		// Host 2
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-placementMessageCh2:
				if ctx.Err() != nil {
					return
				}

				assert.Len(c, placementTables.GetEntries(), 2)
				assert.Contains(c, placementTables.GetEntries(), "actor2")
				assert.Contains(c, placementTables.GetEntries(), "actor3")
			}
		}, 10*time.Second, 10*time.Millisecond)
	})
}
