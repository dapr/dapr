// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/placement/raft"
)

func TestNewConfig(t *testing.T) {
	t.Run("TestNewConfig with default value", func(t *testing.T) {
		cfg := newConfig()
		assert.Equal(t, cfg.raftID, "dapr-placement-0")
		assert.Equal(t, cfg.raftPeerString, "dapr-placement-0=127.0.0.1:8201")
		assert.Equal(t, cfg.raftPeers, []raft.PeerInfo{{ID: "dapr-placement-0", Address: "127.0.0.1:8201"}})
		assert.Equal(t, cfg.raftInMemEnabled, true)
		assert.Equal(t, cfg.raftLogStorePath, "")
		assert.Equal(t, cfg.healthzPort, defaultHealthzPort)
		assert.Equal(t, cfg.certChainPath, defaultCredentialsPath)
		assert.Equal(t, cfg.tlsEnabled, false)
		if runtime.GOOS == "windows" {
			assert.Equal(t, cfg.placementPort, defaultPlacementPortOnWin)
		} else {
			assert.Equal(t, cfg.placementPort, defaultPlacementPort)
		}
	})
}

func TestParsePeersFromFlag(t *testing.T) {
	peerAddressTests := []struct {
		in  string
		out []raft.PeerInfo
	}{
		{
			"node0=127.0.0.1:3030",
			[]raft.PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
			},
		}, {
			"node0=127.0.0.1:3030,node1=127.0.0.1:3031,node2=127.0.0.1:3032",
			[]raft.PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
				{ID: "node1", Address: "127.0.0.1:3031"},
				{ID: "node2", Address: "127.0.0.1:3032"},
			},
		}, {
			"127.0.0.1:3030,node1=127.0.0.1:3031,node2=127.0.0.1:3032",
			[]raft.PeerInfo{
				{ID: "node1", Address: "127.0.0.1:3031"},
				{ID: "node2", Address: "127.0.0.1:3032"},
			},
		},
	}

	for _, tt := range peerAddressTests {
		t.Run("parse peers from cmd flag: "+tt.in, func(t *testing.T) {
			peerInfo := parsePeersFromFlag(tt.in)
			assert.EqualValues(t, tt.out, peerInfo)
		})
	}
}
