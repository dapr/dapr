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

// Package leadership tracks the scheduler placement leader as observed on the
// WatchHosts stream, for consumers (the actor placement client) which need to
// connect to the current leader and react to leadership changes.
package leadership

import (
	"sync"
)

// Leadership is a thread safe store of the current scheduler placement
// leader.
type Leadership struct {
	lock        sync.Mutex
	leader      string
	unsupported bool
	changed     chan struct{}
}

func New() *Leadership {
	return &Leadership{
		changed: make(chan struct{}),
	}
}

// Set records the current placement leader address. Empty means no leader is
// currently advertised (no scheduler serves placement, or leadership is in
// flux).
func (l *Leadership) Set(leader string) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if l.leader == leader && !l.unsupported {
		return
	}

	l.leader = leader
	l.unsupported = false
	close(l.changed)
	l.changed = make(chan struct{})
}

// SetUnsupported records that the connected scheduler cluster does not
// support WatchHosts leadership at all (old scheduler).
func (l *Leadership) SetUnsupported() {
	l.lock.Lock()
	defer l.lock.Unlock()

	if l.unsupported {
		return
	}

	l.leader = ""
	l.unsupported = true
	close(l.changed)
	l.changed = make(chan struct{})
}

// Leader returns the current placement leader address ("" when unknown),
// whether the scheduler cluster is known to not support placement leadership,
// and a channel which is closed on the next change.
func (l *Leadership) Leader() (string, bool, <-chan struct{}) {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.leader, l.unsupported, l.changed
}
