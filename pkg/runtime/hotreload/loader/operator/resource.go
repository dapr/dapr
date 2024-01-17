/*
Copyright 2023 The Dapr Authors
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

package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cenkalti/backoff/v4"

	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
)

// resource is a generic implementation of an operator resource loader.
// resource will watch and load resources from the operator service.
type resource[T differ.Resource] struct {
	opClient  operatorpb.OperatorClient
	podName   string
	namespace string

	streamer streamer[T]
	store    store.Store[T]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

// streamer is a generic interface for streaming resources from the operator.
// We need a generic interface because the gRPC methods used for streaming
// differ between resources.
type streamer[T differ.Resource] interface {
	list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error)
	close() error
	recv() (*loader.Event[T], error)
	establish(context.Context, operatorpb.OperatorClient, string, string) error
}

func newResource[T differ.Resource](opts Options, store store.Store[T], streamer streamer[T]) *resource[T] {
	return &resource[T]{
		opClient:  opts.OperatorClient,
		podName:   opts.PodName,
		namespace: opts.Namespace,
		streamer:  streamer,
		store:     store,
		closeCh:   make(chan struct{}),
	}
}

func (r *resource[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	resp, err := backoff.RetryWithData(func() ([][]byte, error) {
		return r.streamer.list(ctx, r.opClient, r.namespace, r.podName)
	}, backoff.WithContext(backoff.NewExponentialBackOff(), ctx))
	if err != nil {
		return nil, err
	}

	remotes := make([]T, len(resp))
	for i, c := range resp {
		var obj T
		if err := json.Unmarshal(c, &obj); err != nil {
			return nil, fmt.Errorf("error deserializing object: %s", err)
		}
		remotes[i] = obj
	}

	return &differ.LocalRemoteResources[T]{
		Local:  r.store.List(),
		Remote: remotes,
	}, nil
}

func (r *resource[T]) Stream(ctx context.Context) (<-chan *loader.Event[T], error) {
	if r.closed.Load() {
		return nil, errors.New("stream is closed")
	}

	if err := r.streamer.establish(ctx, r.opClient, r.namespace, r.podName); err != nil {
		return nil, err
	}

	log.Debugf("stream established with operator")

	eventCh := make(chan *loader.Event[T])
	ctx, cancel := context.WithCancel(ctx)
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		select {
		case <-r.closeCh:
		case <-ctx.Done():
		}
		cancel()
	}()
	go func() {
		defer r.wg.Done()
		r.stream(ctx, eventCh)
	}()

	return eventCh, nil
}

func (r *resource[T]) stream(ctx context.Context, eventCh chan<- *loader.Event[T]) {
	for {
		for {
			event, err := r.streamer.recv()
			if err != nil {
				r.streamer.close()
				// Retry on stream error.
				log.Errorf("Error from operator stream: %s", err)
				break
			}

			select {
			case eventCh <- event:
			case <-ctx.Done():
				return
			}
		}

		for {
			if ctx.Err() != nil {
				return
			}

			if err := backoff.Retry(func() error {
				berr := r.streamer.establish(ctx, r.opClient, r.namespace, r.podName)
				if berr != nil {
					log.Errorf("Failed to establish stream: %s", berr)
				}
				return berr
			}, backoff.WithContext(backoff.NewExponentialBackOff(), ctx)); err != nil {
				log.Errorf("Stream retry failed: %s", err)
				return
			}
		}
	}
}

func (r *resource[T]) close() error {
	defer r.wg.Wait()
	if r.closed.CompareAndSwap(false, true) {
		close(r.closeCh)
	}

	return r.streamer.close()
}
