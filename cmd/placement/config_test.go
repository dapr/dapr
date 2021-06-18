// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/placement/raft"
)

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
