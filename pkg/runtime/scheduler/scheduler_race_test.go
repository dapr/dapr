package scheduler

import (
	"sync"
	"testing"

	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops/connector"
)

func TestCurrentActorTypesRace(t *testing.T) {
	t.Parallel()

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
