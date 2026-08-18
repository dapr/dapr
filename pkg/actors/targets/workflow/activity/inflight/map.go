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

package inflight

import (
	"sync"
	"time"
)

// Map tracks in-flight calls keyed by an opaque string. Callers in this
// package compose keys via Key(actorID, taskEvent), but Map itself does not
// interpret the key. Safe for concurrent use; the zero value is ready to
// use.
type Map struct {
	m sync.Map
}

// Acquire returns the inflight call for key. If the caller is the first
// arrival for this key, owner is true and the caller must schedule the
// WorkItem and eventually call Finish + Release (or ReleaseAfter). Subsequent
// callers (followers) get the same call with owner=false and should wait on
// Done.
func (m *Map) Acquire(key string) (call *Call, owner bool) {
	fresh := newCall()
	actual, loaded := m.m.LoadOrStore(key, fresh)
	return actual.(*Call), !loaded
}

// Release removes the inflight entry for key if it still matches call.
// CompareAndDelete protects against clobbering a follow-on dispatch that
// legitimately reused the slot.
func (m *Map) Release(key string, call *Call) {
	m.m.CompareAndDelete(key, call)
}

// ReleaseAfter schedules a Release for key after the given delay. Used by
// the owner to keep the cached outcome around briefly so cron retries that
// arrive after the owner finishes can still find the entry as a follower and
// ack SUCCESS without re-dispatching.
func (m *Map) ReleaseAfter(key string, call *Call, after time.Duration) {
	time.AfterFunc(after, func() {
		m.Release(key, call)
	})
}
