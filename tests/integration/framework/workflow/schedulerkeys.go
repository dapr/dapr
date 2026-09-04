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

package workflow

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
)

// AssertScheduledTimers asserts the scheduler holds exactly the given timer
// reminders. Under fast path the instance also owns a single new-event-janitor
// reminder once a fast-path drive has happened (janitorArmed); the janitor is
// asserted lazily, so it is absent before the first raised event or activity.
func AssertScheduledTimers(t *testing.T, c *assert.CollectT, ctx context.Context, w *procworkflow.Workflow, janitorArmed bool, timers ...string) {
	t.Helper()

	var janitors, others []string
	for _, key := range w.Scheduler().ListAllKeys(t, ctx, "dapr/jobs") {
		if strings.Contains(key, "new-event-janitor") {
			janitors = append(janitors, key)
		} else {
			others = append(others, key)
		}
	}

	if w.FastPath() && janitorArmed {
		assert.Len(c, janitors, 1)
	} else {
		assert.Empty(c, janitors)
	}

	if assert.Len(c, others, len(timers)) {
		// Key listing order is not guaranteed: match expected timers against
		// the key set as a multiset, consuming one key per expectation.
		remaining := slices.Clone(others)
		for _, timer := range timers {
			idx := slices.IndexFunc(remaining, func(key string) bool {
				return strings.Contains(key, timer)
			})
			if assert.GreaterOrEqual(c, idx, 0, "no scheduler key matches timer %q, keys: %v", timer, remaining) {
				remaining = slices.Delete(remaining, idx, idx+1)
			}
		}
	}
}
