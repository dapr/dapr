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

package namespaces

import (
	"sync"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func (n *namespaces) handleStatePlacement(request *loops.StateTableRequest) {
	resp := &v1pb.StatePlacementTables{
		Tables: make(map[string]*v1pb.StatePlacementTable),
	}

	var lock sync.Mutex
	var wg sync.WaitGroup
	wg.Add(len(n.disseminators))

	for ns, diss := range n.disseminators {
		diss.loop.Enqueue(&loops.NamespaceTableRequest{
			Table: func(result *v1pb.StatePlacementTable) {
				lock.Lock()
				resp.Tables[ns] = result
				lock.Unlock()
				wg.Done()
			},
		})
	}

	wg.Wait()

	request.State(resp)
}
