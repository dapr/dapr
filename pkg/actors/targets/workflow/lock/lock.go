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

	"github.com/dapr/dapr/pkg/actors/targets/errors"
)

type Lock struct {
	ch      chan struct{}
	closeCh chan struct{}
}

func New() *Lock {
	return &Lock{
		ch:      make(chan struct{}, 1),
		closeCh: make(chan struct{}),
	}
}

func (l *Lock) ContextLock(ctx context.Context) (context.CancelFunc, error) {
	select {
	case l.ch <- struct{}{}:
	case <-l.closeCh:
		return nil, errors.NewClosed("lock")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return func() { <-l.ch }, nil
}

func (l *Lock) Init() {
	l.closeCh = make(chan struct{})
}

func (l *Lock) Close() {
	select {
	case <-l.closeCh:
	default:
		close(l.closeCh)
	}
}
