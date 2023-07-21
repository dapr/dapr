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

package placement

type PlacementTables struct {
	HostList     []HostInfo `json:"hostList,omitempty"`
	TableVersion uint64     `json:"tableVersion,omitempty"`
}
type HostInfo struct {
	Name      string   `json:"name,omitempty"`
	AppID     string   `json:"appId,omitempty"`
	Entities  []string `json:"entities,omitempty"`
	UpdatedAt int64    `json:"updatedAt,omitempty"`
}

// GetPlacementTables returns the current placement host infos.
func (p *Service) GetPlacementTables() (*PlacementTables, error) {
	m := p.raftNode.FSM().State().Members()
	version := p.raftNode.FSM().State().TableGeneration()
	response := &PlacementTables{
		TableVersion: version,
	}
	members := make([]HostInfo, 0, len(m))
	// the key of the member map is the host name, so we can just ignore it.
	for _, v := range m {
		members = append(members, HostInfo{
			Name:      v.Name,
			AppID:     v.AppID,
			Entities:  v.Entities,
			UpdatedAt: v.UpdatedAt,
		})
	}
	response.HostList = members
	return response, nil
}
