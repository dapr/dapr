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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type blockingSetBinaryFileStream struct {
	ctx  context.Context
	once sync.Once
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
	first := false
	b.once.Do(func() {
		first = true
	})
	if first {
		req := message.(*runtimev1pb.SetBinaryFileRequest)
		*req = runtimev1pb.SetBinaryFileRequest{
			SetBinaryFileRequestType: &runtimev1pb.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &runtimev1pb.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "missing",
					FileName:      "file.bin",
				},
			},
		}
		return nil
	}

	<-b.ctx.Done()
	return b.ctx.Err()
}

func (b *blockingSetBinaryFileStream) SendAndClose(*runtimev1pb.SetBinaryFileResponse) error {
	return nil
}

func (b *blockingSetBinaryFileStream) Recv() (*runtimev1pb.SetBinaryFileRequest, error) {
	req := new(runtimev1pb.SetBinaryFileRequest)
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
