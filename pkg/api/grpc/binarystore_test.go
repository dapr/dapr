//go:build unit

/*
Copyright 2026 The Dapr Authors
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

package grpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/binarystore"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type blockingSetBinaryFileStream struct {
	ctx            context.Context
	recvErr        error
	blockRecv      bool
	recvStarted    chan struct{}
	initialRequest *runtimev1pb.SetBinaryFileRequestInitialAlpha1
	once           sync.Once
}

type providerErrorBinaryStore struct {
	binarystore.BinaryStore
}

type duplicateInitialSetBinaryFileStream struct {
	blockingSetBinaryFileStream
	messages int
}

func (*providerErrorBinaryStore) Set(_ context.Context, req *binarystore.SetRequest) error {
	_, _ = io.Copy(io.Discard, req.Data)
	return errors.New("provider error")
}

func (d *duplicateInitialSetBinaryFileStream) RecvMsg(message any) error {
	if d.messages == 2 {
		return io.EOF
	}
	d.messages++
	req := message.(*runtimev1pb.SetBinaryFileRequestAlpha1)
	*req = runtimev1pb.SetBinaryFileRequestAlpha1{
		SetBinaryFileRequestType: &runtimev1pb.SetBinaryFileRequestAlpha1_InitialRequest{
			InitialRequest: &runtimev1pb.SetBinaryFileRequestInitialAlpha1{
				ComponentName: "store",
				FileName:      "file.bin",
			},
		},
	}
	return nil
}

func (b *blockingSetBinaryFileStream) SetHeader(metadata.MD) error {
	return nil
}

func (b *blockingSetBinaryFileStream) SendHeader(metadata.MD) error {
	return nil
}

func (b *blockingSetBinaryFileStream) SetTrailer(metadata.MD) {}

func (b *blockingSetBinaryFileStream) Context() context.Context {
	return b.ctx
}

func (b *blockingSetBinaryFileStream) SendMsg(any) error {
	return nil
}

func (b *blockingSetBinaryFileStream) RecvMsg(message any) error {
	if b.blockRecv {
		if b.recvStarted != nil {
			close(b.recvStarted)
			b.recvStarted = nil
		}
		<-b.ctx.Done()
		return b.ctx.Err()
	}
	if b.recvErr != nil {
		return b.recvErr
	}

	first := false
	b.once.Do(func() {
		first = true
	})
	if first {
		initialRequest := b.initialRequest
		if initialRequest == nil {
			initialRequest = &runtimev1pb.SetBinaryFileRequestInitialAlpha1{
				ComponentName: "missing",
				FileName:      "file.bin",
			}
		}
		req := message.(*runtimev1pb.SetBinaryFileRequestAlpha1)
		*req = runtimev1pb.SetBinaryFileRequestAlpha1{
			SetBinaryFileRequestType: &runtimev1pb.SetBinaryFileRequestAlpha1_InitialRequest{
				InitialRequest: initialRequest,
			},
		}
		return nil
	}

	<-b.ctx.Done()
	return b.ctx.Err()
}

func (b *blockingSetBinaryFileStream) SendAndClose(*runtimev1pb.SetBinaryFileResponseAlpha1) error {
	return nil
}

func (b *blockingSetBinaryFileStream) Recv() (*runtimev1pb.SetBinaryFileRequestAlpha1, error) {
	req := new(runtimev1pb.SetBinaryFileRequestAlpha1)
	err := b.RecvMsg(req)
	return req, err
}

func TestSetBinaryFileReturnsComponentErrorBeforeClientClosesStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &api{
		Universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("dapr.runtime.grpc.test"),
			Resiliency: resiliency.New(nil),
			CompStore:  compstore.New(),
		}),
		logger: logger.NewLogger("dapr.runtime.grpc.test"),
	}
	defer a.wg.Wait()
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- a.SetBinaryFileAlpha1(&blockingSetBinaryFileStream{ctx: ctx})
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, messages.ErrBinaryStoreNotFound)
	case <-time.After(time.Second):
		t.Fatal("SetBinaryFileAlpha1 waited for the client to close the stream")
	}
}

func TestSetBinaryFileUsesUniversalValidation(t *testing.T) {
	tests := []struct {
		name           string
		initialRequest *runtimev1pb.SetBinaryFileRequestInitialAlpha1
		expected       error
	}{
		{
			name: "empty component name",
			initialRequest: &runtimev1pb.SetBinaryFileRequestInitialAlpha1{
				FileName: "file.bin",
			},
			expected: messages.ErrBinaryStoreNotFound,
		},
		{
			name: "empty file name",
			initialRequest: &runtimev1pb.SetBinaryFileRequestInitialAlpha1{
				ComponentName: "missing",
			},
			expected: messages.ErrBinaryStoreNameMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			a := &api{
				Universal: universal.New(universal.Options{
					Logger:     logger.NewLogger("dapr.runtime.grpc.test"),
					Resiliency: resiliency.New(nil),
					CompStore:  compstore.New(),
				}),
				logger: logger.NewLogger("dapr.runtime.grpc.test"),
			}
			defer a.wg.Wait()
			defer cancel()

			err := a.SetBinaryFileAlpha1(&blockingSetBinaryFileStream{
				ctx:            ctx,
				initialRequest: test.initialRequest,
			})
			require.ErrorIs(t, err, test.expected)
		})
	}
}

func TestSetBinaryFileReturnsInvalidArgumentForMalformedStreamWhenComponentReturnsProviderError(t *testing.T) {
	compStore := compstore.New()
	compStore.AddBinaryStore("store", &providerErrorBinaryStore{})
	a := &api{
		Universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("dapr.runtime.grpc.test"),
			Resiliency: resiliency.New(nil),
			CompStore:  compStore,
		}),
		logger: logger.NewLogger("dapr.runtime.grpc.test"),
	}
	defer a.wg.Wait()

	stream := &duplicateInitialSetBinaryFileStream{
		blockingSetBinaryFileStream: blockingSetBinaryFileStream{
			ctx: context.Background(),
		},
	}
	err := a.SetBinaryFileAlpha1(stream)

	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid request: initial request found in non-leading message", status.Convert(err).Message())
}

func TestSetBinaryFileReturnsAPIClosed(t *testing.T) {
	a := &api{
		logger: logger.NewLogger("dapr.runtime.grpc.test"),
		closed: true,
	}

	err := a.SetBinaryFileAlpha1(&blockingSetBinaryFileStream{ctx: context.Background()})
	require.ErrorIs(t, err, errAPIClosed)
}

func TestSetBinaryFileRejectsEmptyStream(t *testing.T) {
	a := &api{
		logger: logger.NewLogger("dapr.runtime.grpc.test"),
	}
	defer a.wg.Wait()

	err := a.SetBinaryFileAlpha1(&blockingSetBinaryFileStream{
		ctx:     context.Background(),
		recvErr: io.EOF,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid request: error receiving the first message: EOF", status.Convert(err).Message())
}

func TestSetBinaryFileCloseReleasesPendingFirstChunkReceive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := newCloseTestAPI()
	a.logger = logger.NewLogger("dapr.runtime.grpc.test")

	recvStarted := make(chan struct{})
	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- a.SetBinaryFileAlpha1(&blockingSetBinaryFileStream{ctx: ctx, blockRecv: true, recvStarted: recvStarted})
	}()

	// Wait until the handler is parked in the first-chunk receive; the
	// tracked goroutine is now blocked in RecvMsg, so Close must wait for it.
	select {
	case <-recvStarted:
	case <-time.After(5 * time.Second):
		require.Fail(t, "handler never reached the first-chunk receive")
	}

	closed := make(chan struct{})
	go func() {
		assert.NoError(t, a.Close())
		close(closed)
	}()

	// The handler is parked in the first-chunk receive, so Close must wait
	// for the tracked receive goroutine.
	select {
	case <-closed:
		require.Fail(t, "Close must wait for the pending first-chunk receive")
	case <-time.After(100 * time.Millisecond):
	}

	// gRPC cancels the stream context when the stream closes, which must
	// release the pending receive and let Close return.
	cancel()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		require.Fail(t, "Close did not return after the stream context was canceled")
	}

	require.Equal(t, codes.InvalidArgument, status.Code(<-handlerDone))
}
