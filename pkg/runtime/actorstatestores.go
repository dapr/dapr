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

package runtime

import (
	"github.com/dapr/dapr/pkg/config"
	wfactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
)

// actorStateStoreBindings returns the name of the state store bound to each
// actor type: the types of spec.actors.stateStores, and, if
// spec.workflow.stateStore is set, the app's workflow actor types. The two
// cannot overlap, as spec.actors.stateStores rejects internal actor types.
func actorStateStoreBindings(spec config.ConfigurationSpec, namespace, appID string) map[string]string {
	bindings := spec.ActorsSpec.StateStoreByActorType()
	if store := spec.WorkflowSpec.GetStateStore(); store != "" {
		for _, actorType := range wfactors.NewActorTypes(namespace, appID).All() {
			bindings[actorType] = store
		}
	}
	return bindings
}
