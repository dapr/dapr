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

package placement

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/kit/events/loop"
)

// remoteHosts tells WatchRemoteHosts subscribers whether a daprd other than
// this one hosts an actor type, as the placement tables show it. Every daprd
// receives the tables of its namespace whether or not it hosts actor types.
// Owned by the placement loop, so it needs no lock, and it outlives stream
// sessions: the last answer survives stream loss. It owns the subscriber
// channels: it creates them, is their only sender, and their only closer. The
// zero value is ready to use.
type remoteHosts struct {
	// known is set once a dissemination round completed with an answer.
	known bool

	// remote holds the types a daprd other than this one hosts, as of the
	// last round which settled each.
	remote map[string]bool

	// unsettled are the types no round has settled since known was set:
	// rounds still in progress covered them.
	unsettled map[string]struct{}

	watchers map[string]map[chan bool]struct{}
}

// answer returns whether a daprd other than this one hosts actorType, and
// whether that is known yet.
func (r *remoteHosts) answer(actorType string) (bool, bool) {
	if !r.known {
		return false, false
	}
	if _, ok := r.unsettled[actorType]; ok {
		return false, false
	}
	return r.remote[actorType], true
}

// watch creates a subscriber channel for e.ActorType, sends it the answer at
// once if it is known, and replies with it on e.Resp. Once e.Ctx is done, an
// UnwatchRemoteHosts enqueued on placeLoop closes it. Should the loop have
// stopped by then, the unwatch is never handled, and close has closed the
// channel already.
func (r *remoteHosts) watch(e *loops.WatchRemoteHosts, placeLoop loop.Interface[loops.EventPlace]) {
	ch := make(chan bool, 1)
	if r.watchers == nil {
		r.watchers = make(map[string]map[chan bool]struct{})
	}
	if r.watchers[e.ActorType] == nil {
		r.watchers[e.ActorType] = make(map[chan bool]struct{})
	}
	r.watchers[e.ActorType][ch] = struct{}{}

	if remote, ok := r.answer(e.ActorType); ok {
		send(ch, remote)
	}

	context.AfterFunc(e.Ctx, func() {
		placeLoop.Enqueue(&loops.UnwatchRemoteHosts{ActorType: e.ActorType, Ch: ch})
	})
	e.Resp <- ch
}

// unwatch ends the subscription of ch and closes it. It is a no-op if ch was
// already closed by close.
func (r *remoteHosts) unwatch(actorType string, ch chan bool) {
	chs, ok := r.watchers[actorType]
	if !ok {
		return
	}
	if _, ok := chs[ch]; !ok {
		return
	}
	delete(chs, ch)
	if len(chs) == 0 {
		delete(r.watchers, actorType)
	}
	close(ch)
}

// roundCompleted records what a completed round showed, and sends the
// subscribers of each type whose answer it settled or changed. A pending type
// keeps the answer of the last round which settled it.
func (r *remoteHosts) roundCompleted(e *loops.RoundCompleted) {
	if e.PendingAll {
		return
	}

	before := make(map[string][2]bool, len(r.watchers))
	for actorType := range r.watchers {
		remote, ok := r.answer(actorType)
		before[actorType] = [2]bool{remote, ok}
	}

	remote := e.Remote
	if remote == nil {
		remote = make(map[string]bool)
	}
	unsettled := make(map[string]struct{})
	for actorType := range e.Pending {
		delete(remote, actorType)
		if prev, ok := r.answer(actorType); !ok {
			unsettled[actorType] = struct{}{}
		} else if prev {
			remote[actorType] = true
		}
	}
	r.known, r.remote, r.unsettled = true, remote, unsettled

	for actorType, chs := range r.watchers {
		now, ok := r.answer(actorType)
		if !ok || before[actorType] == [2]bool{now, true} {
			continue
		}
		for ch := range chs {
			send(ch, now)
		}
	}
}

// close closes every subscriber channel, at shutdown.
func (r *remoteHosts) close() {
	for _, chs := range r.watchers {
		for ch := range chs {
			close(ch)
		}
	}
	r.watchers = nil
}

// send replaces an answer the receiver has not taken yet, so a slow receiver
// gets the latest answer only. The placement loop is the only sender on ch,
// and ch is still open while it is in watchers, so the send cannot block.
func send(ch chan bool, remote bool) {
	select {
	case <-ch:
	default:
	}
	ch <- remote
}
