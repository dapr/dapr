/*
Copyright 2021 The Dapr Authors
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

package options

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
