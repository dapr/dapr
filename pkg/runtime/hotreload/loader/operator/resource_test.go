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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
)

func Test_generic(t *testing.T) {
	t.Run("Stream should return error on stream when already closed", func(t *testing.T) {
		streamer := newFakeStreamer()
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponent(compstore.New()),
			streamer,
		)

		require.NoError(t, r.close())
		ch, err := r.Stream(context.Background())
		assert.Nil(t, ch)
		require.ErrorContains(t, err, "stream is closed")
	})

	t.Run("Stream should return error on stream and context cancelled", func(t *testing.T) {
		streamer := newFakeStreamer()
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponent(compstore.New()),
			streamer,
		)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		streamer.establishFn = func(context.Context, operatorpb.OperatorClient, string, string) error {
			return errors.New("test error")
		}

		ch, err := r.Stream(ctx)
		assert.Nil(t, ch)
		require.ErrorContains(t, err, "test error")
	})

	t.Run("Should send event to Stream channel on Recv", func(t *testing.T) {
		streamer := newFakeStreamer()
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponent(compstore.New()),
			streamer,
		)

		recCh := make(chan *loader.Event[componentsapi.Component], 1)
		streamer.recvFn = func() (*loader.Event[componentsapi.Component], error) {
			return <-recCh, nil
		}

		ch, err := r.Stream(context.Background())
		assert.NotNil(t, ch)
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			comp := new(loader.Event[componentsapi.Component])
			select {
			case recCh <- comp:
			case <-time.After(time.Second):
				t.Error("expected to be able to send on receive")
			}

			select {
			case got := <-ch:
				assert.Same(t, comp, got)
			case <-time.After(time.Second):
				t.Error("expected to get event from on receive")
			}
		}

		close(recCh)

		require.NoError(t, r.close())
	})

	t.Run("Should attempt to re-establish after the stream fails", func(t *testing.T) {
		streamer := newFakeStreamer()
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponent(compstore.New()),
			streamer,
		)

		var calls int
		retried := make(chan struct{})
		streamer.establishFn = func(context.Context, operatorpb.OperatorClient, string, string) error {
			defer func() { calls++ }()

			if calls == 3 {
				close(retried)
			}

			if calls == 0 || calls > 2 {
				return nil
			}

			return errors.New("test error")
		}

		streamer.recvFn = func() (*loader.Event[componentsapi.Component], error) {
			return nil, errors.New("recv error")
		}

		_, err := r.Stream(context.Background())
		require.NoError(t, err)

		select {
		case <-retried:
		case <-time.After(time.Second * 3):
			t.Error("expected generic to retry establishing stream after failure")
		}

		require.NoError(t, r.close())
		assert.GreaterOrEqual(t, calls, 3)
	})

	t.Run("close waits for streamer to close", func(t *testing.T) {
		streamer := newFakeStreamer()
		r := newResource[componentsapi.Component](
			Options{},
			loadercompstore.NewComponent(compstore.New()),
			streamer,
		)

		closeCh := make(chan error)
		streamer.closeFn = func() error {
			closeCh <- errors.New("streamer error")
			return errors.New("return error")
		}

		go func() {
			closeCh <- r.close()
		}()

		select {
		case err := <-closeCh:
			require.ErrorContains(t, err, "streamer error")
		case <-time.After(time.Second * 3):
			t.Error("streamer did not close in time")
		}

		select {
		case err := <-closeCh:
			require.ErrorContains(t, err, "return error")
		case <-time.After(time.Second * 3):
			t.Error("generic did not close in time")
		}
	})
}

type fakeStreamer[T differ.Resource] struct {
	listFn      func(context.Context, operatorpb.OperatorClient, string, string) ([][]byte, error)
	closeFn     func() error
	recvFn      func() (*loader.Event[T], error)
	establishFn func(context.Context, operatorpb.OperatorClient, string, string) error
}

func newFakeStreamer() *fakeStreamer[componentsapi.Component] {
	return &fakeStreamer[componentsapi.Component]{
		listFn: func(context.Context, operatorpb.OperatorClient, string, string) ([][]byte, error) {
			return nil, nil
		},
		closeFn: func() error {
			return nil
		},
		recvFn: func() (*loader.Event[componentsapi.Component], error) {
			return nil, nil
		},
		establishFn: func(context.Context, operatorpb.OperatorClient, string, string) error {
			return nil
		},
	}
}

//nolint:unused
func (f *fakeStreamer[T]) list(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) ([][]byte, error) {
	return f.listFn(ctx, opclient, ns, podName)
}

//nolint:unused
func (f *fakeStreamer[T]) close() error {
	return f.closeFn()
}

//nolint:unused
func (f *fakeStreamer[T]) recv() (*loader.Event[T], error) {
	return f.recvFn()
}

//nolint:unused
func (f *fakeStreamer[T]) establish(ctx context.Context, opclient operatorpb.OperatorClient, ns, podName string) error {
	return f.establishFn(ctx, opclient, ns, podName)
}
