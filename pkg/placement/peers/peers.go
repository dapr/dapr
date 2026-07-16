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

package peers

import (
	"fmt"

	"github.com/hashicorp/raft"
)

// PeerInfo represents raft peer node information.
type PeerInfo struct {
	ID      string
	Address string
}

func AddressForID(id string, nodes []PeerInfo) (string, error) {
	for _, node := range nodes {
		if node.ID == id {
			return node.Address, nil
		}
	}

	return "", fmt.Errorf("address for node %s not found in raft peers %s", id, nodes)
}

func BootstrapConfig(peers []PeerInfo) (*raft.Configuration, error) {
	raftConfig := &raft.Configuration{
		Servers: make([]raft.Server, len(peers)),
	}

	for i, p := range peers {
		raftConfig.Servers[i] = raft.Server{
			ID:      raft.ServerID(p.ID),
			Address: raft.ServerAddress(p.Address),
		}
	}

	return raftConfig, nil
}
