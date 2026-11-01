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
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

const Header = "Dapr-Actor-Activation-Id"

func FromHeader(r *http.Request) string {
	return r.Header.Get(Header)
}

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

func FirstIDs(events []Event) map[string]string {
	ids := make(map[string]string)
	for _, e := range events {
		if _, ok := ids[e.Kind]; !ok {
			ids[e.Kind] = e.ID
		}
	}
	return ids
}

// Cache mimics how SDKs cache actor state per activation.
type Cache struct {
	lock    sync.Mutex
	entries map[string]entry
}

type entry struct {
	value        int
	activationID string
}

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

func (c *Cache) Delete(actorID string) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	_, ok := c.entries[actorID]
	delete(c.entries, actorID)
	return ok
}

func (c *Cache) Value(actorID string) int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.entries[actorID].value
}

// Invoke retries until daprd can place the actor.
func Invoke(t *testing.T, ctx context.Context, client rtv1.DaprClient, req *rtv1.InvokeActorRequest) []byte {
	t.Helper()

	var data []byte
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.InvokeActor(ctx, req)
		if assert.NoError(c, err) {
			data = resp.GetData()
		}
	}, time.Second*8, time.Millisecond*10)
	return data
}
