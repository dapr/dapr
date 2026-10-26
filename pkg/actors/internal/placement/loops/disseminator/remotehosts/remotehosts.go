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
	lock     sync.Mutex
	watchers map[string]map[*watcher]struct{}
}

type watcher struct {
	ch    chan bool
	known bool
	last  bool
}

func New() *Watch {
	return &Watch{
		watchers: make(map[string]map[*watcher]struct{}),
	}
}

// Subscribe returns a channel which receives whether a daprd other than this
// one hosts actorType: at the end of the next dissemination round, or at
// once if a round has completed since an earlier subscriber subscribed to
// the same type, and then whenever the answer changes. Only the latest
// answer is kept for a slow receiver. The channel is closed once ctx is done.
func (w *Watch) Subscribe(ctx context.Context, actorType string) <-chan bool {
	wt := &watcher{ch: make(chan bool, 1)}

	w.lock.Lock()
	set, ok := w.watchers[actorType]
	if !ok {
		set = make(map[*watcher]struct{})
		w.watchers[actorType] = set
	}
	for other := range set {
		if other.known {
			wt.known, wt.last = true, other.last
			wt.ch <- wt.last
			break
		}
	}
	set[wt] = struct{}{}
	w.lock.Unlock()

	context.AfterFunc(ctx, func() {
		w.lock.Lock()
		defer w.lock.Unlock()
		delete(w.watchers[actorType], wt)
		if len(w.watchers[actorType]) == 0 {
			delete(w.watchers, actorType)
		}
		close(wt.ch)
	})

	return wt.ch
}

// Update sends hasRemote's answer for each watched actor type to the
// subscribers which have not received it yet. It never blocks.
func (w *Watch) Update(hasRemote func(actorType string) bool) {
	w.lock.Lock()
	defer w.lock.Unlock()

	for actorType, set := range w.watchers {
		remote := hasRemote(actorType)
		for wt := range set {
			if wt.known && wt.last == remote {
				continue
			}
			wt.known, wt.last = true, remote
			// Replace an answer the receiver has not taken yet. The lock
			// makes this the only sender, so the send cannot block.
			select {
			case <-wt.ch:
			default:
			}
			wt.ch <- remote
		}
	}
}
