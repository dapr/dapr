/*
Copyright 2024 The Dapr Authors
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

package actors

import (
	"github.com/dapr/kit/ptr"
)

func (a *actorsRuntime) idleProcessorExecuteFn(act *actor) {
	// This function is outlined for testing
	if !a.idleActorBusyCheck(act) {
		return
	}

	// Proceed with deactivating the actor
	err := a.deactivateActor(act)
	if err != nil {
		log.Errorf("Failed to deactivate actor %s: %v", act.Key(), err)
	}
}

func (a *actorsRuntime) idleActorBusyCheck(act *actor) bool {
	// If the actor is still busy, we will increase its idle time and re-enqueue it
	if !act.isBusy() {
		return true
	}

	act.idleAt.Store(ptr.Of(a.clock.Now().Add(actorBusyReEnqueueInterval)))
	a.idleActorProcessor.Enqueue(act)
	return false
}
