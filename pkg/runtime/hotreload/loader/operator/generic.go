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

type generic[T differ.Resource] struct {
	opClient  operatorpb.OperatorClient
	podName   string
	namespace string

	streamer streamer[T]
	store    store.Store[T]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

type streamer[T differ.Resource] interface {
	list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error)
	close() error
	recv() (*loader.Event[T], error)
	establish(context.Context, operatorpb.OperatorClient, string, string) error
}

func newGeneric[T differ.Resource](opts Options, store store.Store[T], streamer streamer[T]) *generic[T] {
	return &generic[T]{
		opClient:  opts.OperatorClient,
		podName:   opts.PodName,
		namespace: opts.Namespace,
		streamer:  streamer,
		store:     store,
		closeCh:   make(chan struct{}),
	}
}

func (g *generic[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	resp, err := backoff.RetryWithData(func() ([][]byte, error) {
		return g.streamer.list(ctx, g.opClient, g.namespace, g.podName)
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
		Local:  g.store.List(),
		Remote: remotes,
	}, nil
}

func (g *generic[T]) Stream(ctx context.Context) (<-chan *loader.Event[T], error) {
	if g.closed.Load() {
		return nil, errors.New("stream is closed")
	}

	if err := g.streamer.establish(ctx, g.opClient, g.namespace, g.podName); err != nil {
		return nil, err
	}

	log.Debugf("stream established with operator")

	eventCh := make(chan *loader.Event[T])
	ctx, cancel := context.WithCancel(ctx)
	g.wg.Add(2)
	go func() {
		defer g.wg.Done()
		select {
		case <-g.closeCh:
		case <-ctx.Done():
		}
		cancel()
	}()
	go func() {
		defer g.wg.Done()
		g.stream(ctx, eventCh)
	}()

	return eventCh, nil
}

func (g *generic[T]) stream(ctx context.Context, eventCh chan<- *loader.Event[T]) {
	for {
		for {
			event, err := g.streamer.recv()
			if err != nil {
				g.streamer.close()
				// Retry on stream error.
				log.Errorf("error from operator stream: %s", err)
				break
			}

			select {
			case eventCh <- event:
			case <-ctx.Done():
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := backoff.Retry(func() error {
				berr := g.streamer.establish(ctx, g.opClient, g.namespace, g.podName)
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

func (g *generic[T]) close() error {
	defer g.wg.Wait()
	if g.closed.CompareAndSwap(false, true) {
		close(g.closeCh)
	}

	return g.streamer.close()
}
