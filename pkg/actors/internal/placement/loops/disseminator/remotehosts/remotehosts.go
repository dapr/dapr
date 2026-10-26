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

// Package remotehosts tells subscribers whether a daprd other than this one
// hosts an actor type, as this daprd's placement tables show it. Every daprd
// receives the placement tables of its namespace, whether or not it hosts any
// actor type, so it can see which types other daprds host without hosting
// them itself.
package remotehosts

import (
	"context"
	"sync"
)

// Watch holds the subscribers. Update is called by the disseminator once a
// dissemination round completed.
type Watch struct {
	lock  sync.Mutex
	types map[string]*entry
}

// entry is the state of one watched actor type: the last answer sent to its
// subscribers, once one is known, and the subscribers.
type entry struct {
	known bool
	last  bool
	chs   map[chan bool]struct{}
}

func New() *Watch {
	return &Watch{
		types: make(map[string]*entry),
	}
}

// Subscribe returns a channel which receives whether a daprd other than this
// one hosts actorType: at the end of the next dissemination round, or at
// once if a round has completed since an earlier subscriber subscribed to
// the same type, and then whenever the answer changes. Only the latest
// answer is kept for a slow receiver. The channel is closed once ctx is done.
func (w *Watch) Subscribe(ctx context.Context, actorType string) <-chan bool {
	ch := make(chan bool, 1)

	w.lock.Lock()
	e, ok := w.types[actorType]
	if !ok {
		e = &entry{chs: make(map[chan bool]struct{})}
		w.types[actorType] = e
	}
	if e.known {
		ch <- e.last
	}
	e.chs[ch] = struct{}{}
	w.lock.Unlock()

	context.AfterFunc(ctx, func() {
		w.lock.Lock()
		defer w.lock.Unlock()
		delete(e.chs, ch)
		if len(e.chs) == 0 {
			delete(w.types, actorType)
		}
		close(ch)
	})

	return ch
}

// Update sends hasRemote's answer for each watched actor type to its
// subscribers if it changed. A type for which hasRemote reports no settled
// answer is skipped, and its subscribers keep their last answer. It never
// blocks.
func (w *Watch) Update(hasRemote func(actorType string) (remote, settled bool)) {
	w.lock.Lock()
	defer w.lock.Unlock()

	for actorType, e := range w.types {
		remote, settled := hasRemote(actorType)
		if !settled || (e.known && e.last == remote) {
			continue
		}
		e.known, e.last = true, remote
		for ch := range e.chs {
			// Replace an answer the receiver has not taken yet. The lock
			// makes this the only sender, so the send cannot block.
			select {
			case <-ch:
			default:
			}
			ch <- remote
		}
	}
}
