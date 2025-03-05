/*
Copyright 2025 The Dapr Authors
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

package locker

import (
	"context"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/locker"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/concurrency/fifo"
)

type Options struct {
	ConfigStore *reentrancystore.Store
}

type lockeri struct {
	configStore *reentrancystore.Store

	lock  *fifo.Mutex
	locks map[string]*lock
}

func New(opts Options) locker.Interface {
	return &lockeri{
		locks:       make(map[string]*lock),
		lock:        fifo.New(),
		configStore: opts.ConfigStore,
	}
}

func (l *lockeri) Lock(actorType, actorID string) (context.CancelFunc, error) {
	lock := l.getLock(actorType, actorID)
	return lock.baseLock()
}

func (l *lockeri) LockRequest(req *internalv1pb.InternalInvokeRequest) (context.CancelFunc, error) {
	lock := l.getLock(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	return lock.lockRequest(req)
}

func (l *lockeri) getLock(actorType, actorID string) *lock {
	l.lock.Lock()
	defer l.lock.Unlock()

	key := actorType + "||" + actorID

	lockl, ok := l.locks[key]
	if !ok {
		lopts := lockOptions{actorType: actorType}

		ree, ok := l.configStore.Load(actorType)
		if ok {
			lopts.reentrancyEnabled = ree.Enabled
			if ree.MaxStackDepth != nil {
				lopts.maxStackDepth = *ree.MaxStackDepth
			}
		}

		lockl = newLock(lopts)
		l.locks[key] = lockl
	}

	return lockl
}

func (l *lockeri) Close(actorKey string) {
	l.lock.Lock()
	lockl, ok := l.locks[actorKey]
	delete(l.locks, actorKey)
	l.lock.Unlock()
	if !ok {
		return
	}

	lockl.close()
}

func (l *lockeri) CloseUntil(actorKey string, d time.Duration) {
	l.lock.Lock()
	lockl, ok := l.locks[actorKey]
	delete(l.locks, actorKey)
	l.lock.Unlock()
	if !ok {
		return
	}

	lockl.closeUntil(d)
}
