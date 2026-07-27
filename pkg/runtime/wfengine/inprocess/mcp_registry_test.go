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

package inprocess

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/durabletask-go/task"
)

// TestMCPRegistry_UnregisterReportsWasRegistered locks the contract that
// callers (wfengine) rely on to avoid over-decrementing their refcount: an
// unregister on a name that was never registered (or already unregistered)
// must return false.
func TestMCPRegistry_UnregisterReportsWasRegistered(t *testing.T) {
	r := newMCPRegistry(task.NewTaskRegistry())

	// Name that was never seen: false.
	assert.False(t, r.unregister("never-registered"))

	// entry() creates an entry but leaves holder=nil. unregister must still
	// report false because no holder was installed.
	r.entry("pending")
	assert.False(t, r.unregister("pending"))
}

// TestMCPRegistry_CloseAllNoEntries verifies closeAll on an empty registry
// is a no-op and does not invoke onClose.
func TestMCPRegistry_CloseAllNoEntries(t *testing.T) {
	r := newMCPRegistry(task.NewTaskRegistry())

	var called int
	r.closeAll(func(string) { called++ })

	assert.Zero(t, called)
}

// TestMCPRegistry_CloseAllSkipsUnregisteredEntries verifies that entries that
// exist (e.g. because entry() was called) but have no holder do not trigger
// onClose. This matches the wasRegistered semantics so the wfengine teardown
// callback isn't invoked for never-installed servers.
func TestMCPRegistry_CloseAllSkipsUnregisteredEntries(t *testing.T) {
	r := newMCPRegistry(task.NewTaskRegistry())

	r.entry("a")
	r.entry("b")

	var called []string
	r.closeAll(func(name string) { called = append(called, name) })

	assert.Empty(t, called)
}

// TestMCPRegistry_CloseAllRace is a -race regression for the shutdown race
// fixed in this PR. Pre-fix, closeAll read each entry's holder field without
// holding the entry mutex while register/unregister wrote it under the
// mutex. Post-fix, closeAll acquires entry.mu before reading.
//
// We don't need real MCP holders to expose the race: the data race is on the
// `entry.holder` field, not on Close() side effects. Goroutines toggle the
// field under entry.mu (mimicking the register/unregister path) while a
// concurrent closeAll loop reads it. -race flags the unsynchronized read
// on master.
func TestMCPRegistry_CloseAllRace(t *testing.T) {
	r := newMCPRegistry(task.NewTaskRegistry())

	const names = 16
	for i := range names {
		r.entry(fmt.Sprintf("server-%d", i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: hold entry.mu and toggle holder (set to a sentinel non-nil
	// pointer that we never actually call Close() on, then back to nil).
	// closeAll's read of entry.holder must be synchronized with these writes.
	for i := range names {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("server-%d", idx)
			e := r.entry(name)
			for {
				select {
				case <-stop:
					return
				default:
				}
				e.mu.Lock()
				e.holder = nil
				e.mu.Unlock()
			}
		}(i)
	}

	// Readers: hammer closeAll. Pass a no-op onClose so we don't depend on
	// any per-entry side effects.
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				r.closeAll(func(string) {})
			}
		})
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}
