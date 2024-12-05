/*
Copyright 2024 The Dapr Authors
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

package lock

import (
	"sync"

	"github.com/dapr/kit/concurrency/fifo"
)

type Lock struct {
	tableLock   *fifo.Mutex
	inTableLock bool
	lookupLock  sync.RWMutex
}

func New() *Lock {
	return &Lock{
		tableLock: fifo.New(),
	}
}

func (l *Lock) LockTable() {
	l.tableLock.Lock()
	defer l.tableLock.Unlock()
	l.lookupLock.Lock()
	l.inTableLock = true
}

func (l *Lock) EnsureUnlockTable() {
	l.tableLock.Lock()
	defer l.tableLock.Unlock()
	if l.inTableLock {
		l.inTableLock = false
		l.lookupLock.Unlock()
	}
}

func (l *Lock) LockLookup() {
	l.lookupLock.RLock()
}

func (l *Lock) UnlockLookup() {
	l.lookupLock.RUnlock()
}
