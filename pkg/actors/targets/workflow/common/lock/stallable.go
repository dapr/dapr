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

package lock

import (
	"context"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/kit/ptr"
)

type Stallable struct {
	*Lock
	stalledCh atomic.Pointer[chan struct{}]
}

func NewStallable() *Stallable {
	return &Stallable{
		Lock:      New(),
		stalledCh: atomic.Pointer[chan struct{}]{},
	}
}

func (l *Stallable) ContextLock(ctx context.Context) (context.CancelFunc, error) {
	stalledCh := l.stalledCh.Load()
	select {
	case l.ch <- struct{}{}:
	case <-l.closeCh:
		return nil, errors.NewClosed("lock")
	case <-*stalledCh:
		return nil, errors.NewStalled()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return func() { <-l.ch }, nil
}

func (l *Stallable) Init() {
	l.Lock.Init()
	l.resetStalledChannel()
}

func (l *Stallable) Stall() context.CancelFunc {
	stalledCh := l.stalledCh.Load()
	select {
	case <-*stalledCh:
	default:
		close(*stalledCh)
	}
	return l.resetStalledChannel
}

func (l *Stallable) resetStalledChannel() {
	l.stalledCh.Store(ptr.Of(make(chan struct{})))
}
