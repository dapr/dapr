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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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
	// closed is set once Run is shutting down. After this point WatchUpdates
	// refuses to register new watchers, so no watcher can be added after Run has
	// closed the existing ones (which would leak a never-closed channel and hang
	// the stream handler, and therefore the server's graceful shutdown).
	closed bool

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
	// Mark shutting down and close all watchers on every exit path, so a
	// concurrent WatchUpdates can never register a watcher that outlives Run
	// (which would leak a never-closed channel and hang the stream handler).
	defer i.shutdown()

	var zero T
	informer, err := i.cache.GetInformer(ctx, zero.ClientObject())
	if err != nil {
		// Ignore errors to prevent fatal shutdown in the case of the operator
		// shutting down early.
		if ctx.Err() != nil {
			return nil
		}

		// If the CRD for this resource type is not installed, log a warning and
		// run as a no-op rather than crashing the operator. This supports
		// version-skew scenarios where newer control plane binaries run against a
		// cluster that doesn't yet have all CRDs.
		if apimeta.IsNoMatchError(err) {
			i.log.Warnf("CRD for %s/%s not found, skipping informer: %v", zero.APIVersion(), zero.Kind(), err)
			<-ctx.Done()
			return nil
		}

		return fmt.Errorf("unable to get setup %s informer: %w", zero.Kind(), err)
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			// Adds emitted while the informer cache is doing its initial sync
			// represent resources that already exist. A sidecar that connects
			// during this window loads that state itself via its own List, so
			// forwarding these as CREATED events is redundant and, for
			// SIGHUP-reload resources (e.g. a foreign Configuration), causes a
			// spurious restart. Only forward genuine post-sync creates.
			if isInInitialList {
				return
			}
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
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("unable to add %s informer event handler: %w", zero.Kind(), err)
	}

	<-ctx.Done()

	return nil
}

// shutdown marks the informer as closed and closes every registered watcher's
// channel. After this, WatchUpdates refuses to register new watchers.
func (i *informer[T]) shutdown() {
	i.lock.Lock()
	defer i.lock.Unlock()
	i.closed = true
	for idx, w := range i.watchers {
		delete(i.watchers, idx)
		close(w.ch)
	}
}

func (i *informer[T]) WatchUpdates(ctx context.Context, ns string) (<-chan *Event[T], context.CancelFunc, error) {
	id, err := authz.Request(ctx, ns)
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan *Event[T], 10)

	i.lock.Lock()
	// Refuse new watchers once Run is shutting down: Run has already closed the
	// existing watchers and will not close any registered after this point.
	if i.closed {
		i.lock.Unlock()
		return nil, nil, status.New(codes.Unavailable, "operator is shutting down").Err()
	}
	idx := i.idx
	i.idx++
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
	if eventType == operatorv1.ResourceEventType_UPDATED && oldObj != nil {
		oldMeta, oerr := apimeta.Accessor(oldObj)
		newMeta, nerr := apimeta.Accessor(newObj)
		if oerr == nil && nerr == nil &&
			oldMeta.GetResourceVersion() != "" &&
			oldMeta.GetResourceVersion() == newMeta.GetResourceVersion() {
			i.log.Debugf("Ignoring resync event for %s/%s: resourceVersion %s unchanged",
				newMeta.GetNamespace(), newMeta.GetName(), newMeta.GetResourceVersion())
			return
		}
	}

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
