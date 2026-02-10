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

package informer

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

type Options struct {
	Cache ctrlcache.Cache
}

// Interface is an interface for syncing Kubernetes manifests.
type Interface[T meta.Resource] interface {
	Run(context.Context) error
	WatchUpdates(context.Context, string) (<-chan *Event[T], context.CancelFunc, error)
}

// Event is a Kubernetes manifest event, containing the manifest and the event
// type.
type Event[T meta.Resource] struct {
	Manifest T
	Type     operatorv1.ResourceEventType
}

type informer[T meta.Resource] struct {
	cache ctrlcache.Cache
	lock  sync.Mutex

	idx      uint64
	watchers map[uint64]*watcher[T]

	log logger.Logger
}

type watcher[T meta.Resource] struct {
	id  *spiffe.Parsed
	ch  chan *Event[T]
	ctx context.Context
}

type informerEvent[T meta.Resource] struct {
	newObj *Event[T]
	oldObj *T
}

func New[T meta.Resource](opts Options) Interface[T] {
	var zero T
	return &informer[T]{
		log:      logger.NewLogger("dapr.operator.informer." + strings.ToLower(zero.Kind())),
		watchers: make(map[uint64]*watcher[T]),
		cache:    opts.Cache,
	}
}

func (i *informer[T]) Run(ctx context.Context) error {
	var zero T
	informer, err := i.cache.GetInformer(ctx, zero.ClientObject())
	if err != nil {
		return fmt.Errorf("unable to get setup %s informer: %w", zero.Kind(), err)
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			i.handleEvent(ctx, nil, obj, operatorv1.ResourceEventType_CREATED)
		},
		UpdateFunc: func(oldObj, newObj any) {
			i.handleEvent(ctx, oldObj, newObj, operatorv1.ResourceEventType_UPDATED)
		},
		DeleteFunc: func(obj any) {
			i.handleEvent(ctx, nil, obj, operatorv1.ResourceEventType_DELETED)
		},
	})
	if err != nil {
		return fmt.Errorf("unable to add %s informer event handler: %w", zero.Kind(), err)
	}

	<-ctx.Done()

	i.lock.Lock()
	defer i.lock.Unlock()
	for idx, w := range i.watchers {
		delete(i.watchers, idx)
		close(w.ch)
	}

	return nil
}

func (i *informer[T]) WatchUpdates(ctx context.Context, ns string) (<-chan *Event[T], context.CancelFunc, error) {
	id, err := authz.Request(ctx, ns)
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan *Event[T], 10)
	idx := i.idx
	i.idx++

	i.lock.Lock()
	i.watchers[idx] = &watcher[T]{
		id:  id,
		ch:  ch,
		ctx: ctx,
	}
	i.lock.Unlock()

	return ch, func() {
		i.lock.Lock()
		defer i.lock.Unlock()
		if _, ok := i.watchers[idx]; ok {
			delete(i.watchers, idx)
			close(ch)
		}
	}, nil
}

func (i *informer[T]) handleEvent(ctx context.Context, oldObj, newObj any, eventType operatorv1.ResourceEventType) {
	newT, ok := i.anyToT(newObj)
	if !ok {
		return
	}

	event := &informerEvent[T]{
		newObj: &Event[T]{
			Manifest: newT,
			Type:     eventType,
		},
	}
	if oldObj != nil {
		oldT, ok := i.anyToT(oldObj)
		if !ok {
			return
		}

		event.oldObj = &oldT
	}

	i.lock.Lock()
	defer i.lock.Unlock()

	for _, w := range i.watchers {
		if ev, ok := appEventFromEvent[T](w.id, event); ok {
			select {
			case <-ctx.Done():
				return
			case w.ch <- ev:
			case <-w.ctx.Done():
				// Watcher has disconnected, skip sending the event.
			}
		}
	}
}

func (i *informer[T]) anyToT(obj any) (T, bool) {
	switch objT := obj.(type) {
	case *T:
		return *objT, true
	case T:
		return objT, true
	case cache.DeletedFinalStateUnknown:
		return i.anyToT(obj.(cache.DeletedFinalStateUnknown).Obj)
	default:
		i.log.Errorf("unexpected type %T", obj)
		var zero T
		return zero, false
	}
}
