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

package inmemory

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
)

// Waves of due-now timers created from many goroutines at once, with the
// queue draining to empty between waves. Every timer in every wave must fire.
func TestConcurrentCreateBurstsAllFire(t *testing.T) {
	const (
		producers = 8
		waves     = 3000
	)

	fired := make(chan string, 1024)
	router := routerfake.New().WithCallReminderFn(
		func(_ context.Context, r *api.Reminder) error {
			fired <- r.ActorID
			return nil
		},
	)

	store := New(Options{Router: router})
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	ctx := context.Background()
	for wave := range waves {
		expected := make(map[string]struct{}, producers)
		timers := make([]*api.Reminder, producers)
		now := clock.RealClock{}.Now()
		for p := range producers {
			id := "w" + strconv.Itoa(wave) + "-p" + strconv.Itoa(p)
			expected[id] = struct{}{}
			timers[p] = newTimer(t, id, "", now)
		}

		var wg sync.WaitGroup
		wg.Add(producers)
		for _, tm := range timers {
			go func() {
				defer wg.Done()
				assert.NoError(t, store.Create(ctx, tm))
			}()
		}
		wg.Wait()

		deadline := time.NewTimer(5 * time.Second)
		for len(expected) > 0 {
			select {
			case id := <-fired:
				delete(expected, id)
			case <-deadline.C:
				require.FailNowf(t, "timers never fired", "wave %d: %d timers not fired: %v", wave, len(expected), expected)
			}
		}
		deadline.Stop()
	}
}
