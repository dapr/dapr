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

package activation

import (
	"net/http"
	"slices"
	"strings"
	"sync"
)

// Header is the callback header and gRPC metadata key daprd sends the
// activation id under.
const Header = "Dapr-Actor-Activation-Id"

// FromHeader returns the activation id of an HTTP actor callback.
func FromHeader(r *http.Request) string {
	return r.Header.Get(Header)
}

// FromMetadata returns the activation id of a gRPC actor callback.
func FromMetadata(md map[string]string) string {
	for k, v := range md {
		if strings.EqualFold(k, Header) {
			return v
		}
	}
	return ""
}

type Event struct {
	Kind string
	ID   string
}

// Recorder records actor callbacks in arrival order.
type Recorder struct {
	lock   sync.Mutex
	events []Event
}

func (r *Recorder) Record(kind, id string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.events = append(r.events, Event{Kind: kind, ID: id})
}

func (r *Recorder) Peek() []Event {
	r.lock.Lock()
	defer r.lock.Unlock()
	return slices.Clone(r.events)
}

func (r *Recorder) Take() []Event {
	r.lock.Lock()
	defer r.lock.Unlock()
	events := r.events
	r.events = nil
	return events
}

// FirstIDs returns the first activation id seen for each kind.
func FirstIDs(events []Event) map[string]string {
	ids := make(map[string]string)
	for _, e := range events {
		if _, ok := ids[e.Kind]; !ok {
			ids[e.Kind] = e.ID
		}
	}
	return ids
}

// Cache holds a value per actor the way an SDK caches actor state, and drops
// a value cached under an older activation.
type Cache struct {
	lock    sync.Mutex
	entries map[string]entry
}

type entry struct {
	value        int
	activationID string
}

// Get returns the cached value only if it was cached under activationID.
func (c *Cache) Get(actorID, activationID string) (int, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	e, ok := c.entries[actorID]
	if !ok || e.activationID != activationID {
		return 0, false
	}
	return e.value, true
}

func (c *Cache) Set(actorID, activationID string, value int) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]entry)
	}
	c.entries[actorID] = entry{value: value, activationID: activationID}
}

// Delete drops the actor's value and reports whether one was cached.
func (c *Cache) Delete(actorID string) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	_, ok := c.entries[actorID]
	delete(c.entries, actorID)
	return ok
}

// Value returns the cached value whatever activation it was cached under.
func (c *Cache) Value(actorID string) int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.entries[actorID].value
}
