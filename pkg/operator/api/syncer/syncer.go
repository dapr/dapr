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

package syncer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/events/batcher"
	"github.com/dapr/kit/logger"
)

type Event[T meta.Resource] struct {
	Manifest T
	Type     operatorv1.ResourceEventType
}

type internalEvent[T meta.Resource] struct {
	newObj *Event[T]
	oldObj *Event[T]
}

type Options struct {
	Manager ctrl.Manager
}

type Syncer[T meta.Resource] struct {
	cache   ctrlcache.Cache
	batcher *batcher.Batcher[int64, *internalEvent[T]]
	batchID atomic.Int64

	log     logger.Logger
	closeCh chan struct{}
	wg      sync.WaitGroup
}

func New[T meta.Resource](opts Options) *Syncer[T] {
	var zero T
	return &Syncer[T]{
		log:     logger.NewLogger("dapr.operator.syncer." + strings.ToLower(zero.Kind())),
		batcher: batcher.New[int64, *internalEvent[T]](0),
		cache:   opts.Manager.GetCache(),
		closeCh: make(chan struct{}),
	}
}

func (s *Syncer[T]) Run(ctx context.Context) error {
	var zero T
	informer, err := s.cache.GetInformer(ctx, zero.ClientObject())
	if err != nil {
		return fmt.Errorf("unable to get setup %s informer: %w", zero.Kind(), err)
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.handleEvent(ctx, nil, obj, operatorv1.ResourceEventType_CREATED)
		},
		UpdateFunc: func(oldObj, newObj any) {
			s.handleEvent(ctx, oldObj, newObj, operatorv1.ResourceEventType_UPDATED)
		},
		DeleteFunc: func(obj any) {
			s.handleEvent(ctx, nil, obj, operatorv1.ResourceEventType_DELETED)
		},
	})
	if err != nil {
		return fmt.Errorf("unable to add %s informer event handler: %w", zero.Kind(), err)
	}

	<-ctx.Done()
	close(s.closeCh)
	s.batcher.Close()
	s.wg.Wait()
	return nil
}
func (s *Syncer[T]) handleEvent(ctx context.Context, oldObj, newObj any, eventType operatorv1.ResourceEventType) {
	newT, ok := s.anyToT(newObj)
	if !ok {
		return
	}

	event := &internalEvent[T]{
		newObj: &Event[T]{
			Manifest: newT,
			Type:     eventType,
		},
	}
	if oldObj != nil {
		oldT, ok := s.anyToT(oldObj)
		if !ok {
			return
		}

		event.oldObj = &Event[T]{
			Manifest: oldT,
			Type:     operatorv1.ResourceEventType_DELETED,
		}
	}

	id := s.batchID.Add(1)
	s.batcher.Batch(id, event)
}

func (s *Syncer[T]) WatchUpdates(ctx context.Context, ns string) (<-chan *Event[T], error) {
	id, err := authz.Request(ctx, ns)
	if err != nil {
		return nil, err
	}

	batchCh := make(chan *internalEvent[T])
	appCh := make(chan *Event[T], 10)

	s.batcher.Subscribe(ctx, batchCh)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.updateLoop(ctx, batchCh, appCh, id)
	}()

	return appCh, nil
}

func (s *Syncer[T]) updateLoop(ctx context.Context, batchCh <-chan *internalEvent[T], appCh chan<- *Event[T], id *spiffe.Parsed) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closeCh:
			return
		case event := <-batchCh:
			if event.newObj.Manifest.GetNamespace() != id.Namespace() {
				continue
			}

			// Handle case where scope is removed from manifest through update.
			var appInOld bool
			if event.oldObj != nil {
				manifest := event.oldObj.Manifest
				appInOld = len(manifest.GetScopes()) == 0 || utils.Contains(manifest.GetScopes(), id.AppID())
			}

			newManifest := event.newObj.Manifest
			var appInNew bool
			if len(newManifest.GetScopes()) == 0 || utils.Contains(newManifest.GetScopes(), id.AppID()) {
				appInNew = true
			}

			var env *Event[T]
			if appInOld && !appInNew {
				env = &Event[T]{
					Manifest: event.oldObj.Manifest,
					Type:     operatorv1.ResourceEventType_DELETED,
				}
			} else {
				env = event.newObj
			}

			select {
			case <-ctx.Done():
			case <-s.closeCh:
			case appCh <- env:
			}
		}
	}
}

func (s *Syncer[T]) anyToT(obj any) (T, bool) {
	switch obj.(type) {
	case *T:
		return *obj.(*T), true
	case T:
		return obj.(T), true
	case cache.DeletedFinalStateUnknown:
		return s.anyToT(obj.(cache.DeletedFinalStateUnknown).Obj)
	default:
		s.log.Errorf("unexpected type %T", obj)
		var zero T
		return zero, false
	}
}
