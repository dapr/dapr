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
	TableVersion uint64     `json:"tableVersion"`
	APILevel     uint32     `json:"apiLevel"`
}
type HostInfo struct {
	Name       string   `json:"name,omitempty"`
	AppID      string   `json:"appId,omitempty"`
	ActorTypes []string `json:"actorTypes,omitempty"`
	UpdatedAt  int64    `json:"updatedAt,omitempty"`
	APILevel   uint32   `json:"apiLevel"`
}

// GetPlacementTables returns the current placement host infos.
func (p *Service) GetPlacementTables() (*PlacementTables, error) {
	state := p.raftNode.FSM().State()
	m := state.Members()
	response := &PlacementTables{
		TableVersion: state.TableGeneration(),
		APILevel:     state.APILevel(),
	}
	if response.APILevel < p.minAPILevel {
		response.APILevel = p.minAPILevel
	}
	if p.maxAPILevel != nil && response.APILevel > *p.maxAPILevel {
		response.APILevel = *p.maxAPILevel
	}
	members := make([]HostInfo, len(m))
	// the key of the member map is the host name, so we can just ignore it.
	var i int
	for _, v := range m {
		members[i] = HostInfo{
			Name:       v.Name,
			AppID:      v.AppID,
			ActorTypes: v.Entities,
			UpdatedAt:  v.UpdatedAt,
			APILevel:   v.APILevel,
		}
		i++
	}
	response.HostList = members
	return response, nil
}
