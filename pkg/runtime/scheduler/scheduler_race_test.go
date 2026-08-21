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

package scheduler

import (
	"sync"
	"testing"

	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops/connector"
)

// TestCurrentActorTypesRace reproduces dapr/dapr#10352: currentActorTypes is
// written by StartApp/StopApp (app-health goroutine) and read/dereferenced by
// ReloadActorTypes (placement disseminator goroutine) without synchronization.
// Run with -race; it must fail before the fix and pass after.
func TestCurrentActorTypesRace(t *testing.T) {
	s := &Scheduler{connector: connector.New(connector.Options{})}

	const iters = 200000

	var wg sync.WaitGroup

	// placement disseminator goroutine: reads/dereferences currentActorTypes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			s.ReloadActorTypes([]string{"actorA", "actorB"})
		}
	}()

	// app-health callback goroutines: write nil into currentActorTypes
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(healthy bool) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if healthy {
					s.StartApp()
				} else {
					s.StopApp()
				}
			}
		}(g%2 == 0)
	}

	wg.Wait()
}
