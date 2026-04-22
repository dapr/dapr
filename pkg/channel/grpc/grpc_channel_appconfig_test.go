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

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/callbackstream"
	"github.com/dapr/dapr/pkg/config"
)

// newChannelWithStream constructs a minimal Channel whose callback stream
// manager can be driven directly by the test. The other fields are left
// zero since GetAppConfig only touches the manager.
func newChannelWithStream(mgr *callbackstream.Manager) *Channel {
	return &Channel{actorCallbackStream: mgr}
}

func TestGetAppConfig_ReturnsRegisteredConfig(t *testing.T) {
	mgr := callbackstream.NewManager()
	c := newChannelWithStream(mgr)

	drainTrue := true
	maxStack := 7
	expected := &config.ApplicationConfig{
		Entities:                []string{"cart", "order"},
		ActorIdleTimeout:        "1h",
		DrainOngoingCallTimeout: "30s",
		DrainRebalancedActors:   &drainTrue,
		Reentrancy:              config.ReentrancyConfig{Enabled: true, MaxStackDepth: &maxStack},
	}

	// Simulate an app opening the stream by registering the config with
	// the manager. GetAppConfig must then return immediately.
	conn := mgr.Register(context.Background(), expected)
	t.Cleanup(func() { mgr.Close(conn, nil) })

	cfg, err := c.GetAppConfig(t.Context(), "app-1")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, expected.Entities, cfg.Entities)
	assert.Equal(t, expected.ActorIdleTimeout, cfg.ActorIdleTimeout)
	assert.Equal(t, expected.DrainOngoingCallTimeout, cfg.DrainOngoingCallTimeout)
	require.NotNil(t, cfg.DrainRebalancedActors)
	assert.True(t, *cfg.DrainRebalancedActors)
	assert.True(t, cfg.Reentrancy.Enabled)
	require.NotNil(t, cfg.Reentrancy.MaxStackDepth)
	assert.Equal(t, 7, *cfg.Reentrancy.MaxStackDepth)
}

// TestGetAppConfig_ReturnsNilWhenNoRegistration verifies that GetAppConfig
// does not block: with no active stream registration it returns (nil, nil)
// immediately so daprd startup is not stalled for apps that don't host
// actors.
func TestGetAppConfig_ReturnsNilWhenNoRegistration(t *testing.T) {
	mgr := callbackstream.NewManager()
	c := newChannelWithStream(mgr)

	cfg, err := c.GetAppConfig(t.Context(), "app-1")
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

// TestCurrentConfig_ReflectsLatestRegistration verifies that
// CurrentConfig exposes the ApplicationConfig advertised by whichever app
// currently holds a stream. Used by the API handler to identify a
// reconnecting app's hosted types.
func TestCurrentConfig_ReflectsLatestRegistration(t *testing.T) {
	mgr := callbackstream.NewManager()
	assert.Nil(t, mgr.CurrentConfig())

	expected := &config.ApplicationConfig{Entities: []string{"orders"}}
	conn := mgr.Register(context.Background(), expected)
	t.Cleanup(func() { mgr.Close(conn, nil) })

	got := mgr.CurrentConfig()
	require.NotNil(t, got)
	assert.Equal(t, []string{"orders"}, got.Entities)
}
