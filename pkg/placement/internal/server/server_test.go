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

package server

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/internal/leadership"
	"github.com/dapr/dapr/pkg/placement/peers"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

func TestKeepaliveEnforcementPolicy(t *testing.T) {
	t.Parallel()

	ports, err := freeport.GetFreePorts(2)
	require.NoError(t, err)

	sec := securityfake.New()
	lead, err := leadership.New(leadership.Options{
		ID:       "node-0",
		Peers:    []peers.PeerInfo{{ID: "node-0", Address: "127.0.0.1:" + strconv.Itoa(ports[1])}},
		Healthz:  healthz.New(),
		Security: sec,
	})
	require.NoError(t, err)

	hz := healthz.New()
	srv := New(Options{
		NodeID:           "node-0",
		Port:             ports[0],
		ListenAddress:    "127.0.0.1",
		Leadership:       lead,
		Security:         sec,
		Healthz:          hz,
		KeepAliveTime:    time.Hour,
		KeepAliveTimeout: 5 * time.Second,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(t.Context()) }()
	t.Cleanup(func() { <-errCh })
	require.Eventually(t, hz.IsReady, 5*time.Second, 10*time.Millisecond)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(ports[0]), time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.Write([]byte(http2.ClientPreface))
	require.NoError(t, err)
	framer := http2.NewFramer(conn, conn)
	require.NoError(t, framer.WriteSettings())

	waitFrame := func(typ http2.FrameType, flags http2.Flags) {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		for {
			f, rerr := framer.ReadFrame()
			require.NoError(t, rerr)
			if ga, ok := f.(*http2.GoAwayFrame); ok {
				require.Failf(t, "server sent GOAWAY", "code=%v debug=%q", ga.ErrCode, string(ga.DebugData()))
			}
			if h := f.Header(); h.Type == typ && h.Flags == flags {
				return
			}
		}
	}

	waitFrame(http2.FrameSettings, 0)
	require.NoError(t, framer.WriteSettingsAck())

	// 4 idle pings spaced >5s apart exceed grpc-go's default maxPingStrikes (2)
	// unless KeepaliveEnforcementPolicy permits them.
	for i, payload := range [][8]byte{{1}, {2}, {3}, {4}} {
		if i > 0 {
			time.Sleep(5200 * time.Millisecond)
		}
		require.NoError(t, framer.WritePing(false, payload))
		waitFrame(http2.FramePing, http2.FlagPingAck)
	}

	// Subsequent SETTINGS round-trip ensures any queued GOAWAY is observed.
	require.NoError(t, framer.WriteSettings())
	waitFrame(http2.FrameSettings, http2.FlagSettingsAck)
}
