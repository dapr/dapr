/*
Copyright 2022 The Dapr Authors
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

package bindings

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"
)

// grpcInputBinding is a implementation of a inputbinding over a gRPC Protocol.
type grpcInputBinding struct {
	*pluggable.GRPCConnector[proto.InputBindingClient]
	bindings.InputBinding
	logger logger.Logger

	closed  atomic.Bool
	wg      sync.WaitGroup
	closeCh chan struct{}
}

// Init initializes the grpc inputbinding passing out the metadata to the grpc component.
func (b *grpcInputBinding) Init(ctx context.Context, metadata bindings.Metadata) error {
	if err := b.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	_, err := b.Client.Init(b.Context, &proto.InputBindingInitRequest{
		Metadata: protoMetadata,
	})
	return err
}

type readHandler = func(*proto.ReadResponse)

// adaptHandler returns a non-error function that handle the message with the given handler and ack when returns.
//
//nolint:nosnakecase
func (b *grpcInputBinding) adaptHandler(ctx context.Context, streamingPull proto.InputBinding_ReadClient, handler bindings.Handler) readHandler {
	safeSend := &sync.Mutex{}
	return func(msg *proto.ReadResponse) {
		var contentType *string
		if len(msg.GetContentType()) != 0 {
			contentType = &msg.ContentType
		}
		m := bindings.ReadResponse{
			Data:        msg.GetData(),
			Metadata:    msg.GetMetadata(),
			ContentType: contentType,
		}

		var respErr *proto.AckResponseError
		bts, err := handler(ctx, &m)
		if err != nil {
			b.logger.Errorf("error when handling message for message: %s", msg.GetMessageId())
			respErr = &proto.AckResponseError{
				Message: err.Error(),
			}
		}

		// As per documentation:
		// When using streams,
		// one must take care to avoid calling either SendMsg or RecvMsg multiple times against the same Stream from different goroutines.
		// In other words, it's safe to have a goroutine calling SendMsg and another goroutine calling RecvMsg on the same stream at the same time.
		// But it is not safe to call SendMsg on the same stream in different goroutines, or to call RecvMsg on the same stream in different goroutines.
		// https://github.com/grpc/grpc-go/blob/master/Documentation/concurrency.md#streams
		safeSend.Lock()
		defer safeSend.Unlock()

		if err := streamingPull.Send(&proto.ReadRequest{
			ResponseData:  bts,
			ResponseError: respErr,
			MessageId:     msg.GetMessageId(),
		}); err != nil {
			b.logger.Errorf("error when ack'ing message %s", msg.GetMessageId())
		}
	}
}

// Read starts a bi-di stream reading messages from component and handling it used the given handler.
func (b *grpcInputBinding) Read(ctx context.Context, handler bindings.Handler) error {
	readStream, err := b.Client.Read(ctx)
	if err != nil {
		return fmt.Errorf("unable to read from binding: %w", err)
	}

	streamCtx, cancel := context.WithCancel(readStream.Context())
	handle := b.adaptHandler(streamCtx, readStream, handler)

	b.wg.Add(2)
	// Cancel on input binding close.
	go func() {
		defer b.wg.Done()
		defer cancel()
		select {
		case <-b.closeCh:
		case <-streamCtx.Done():
		}
	}()
	go func() {
		defer b.wg.Done()
		defer cancel()
		for {
			msg, err := readStream.Recv()
			if err == io.EOF { // no more reads
				return
			}

			// TODO reconnect on error
			if err != nil {
				b.logger.Errorf("failed to receive message: %v", err)
				return
			}
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				handle(msg)
			}()
		}
	}()

	return nil
}

func (b *grpcInputBinding) Close() error {
	defer b.wg.Wait()
	if b.closed.CompareAndSwap(false, true) {
		close(b.closeCh)
	}
	return b.InputBinding.Close()
}

// inputFromConnector creates a new GRPC inputbinding using the given underlying connector.
func inputFromConnector(l logger.Logger, connector *pluggable.GRPCConnector[proto.InputBindingClient]) *grpcInputBinding {
	return &grpcInputBinding{
		GRPCConnector: connector,
		logger:        l,
		closeCh:       make(chan struct{}),
	}
}

// NewGRPCInputBinding creates a new grpc inputbindingusing the given socket factory.
func NewGRPCInputBinding(l logger.Logger, socket string) *grpcInputBinding {
	return inputFromConnector(l, pluggable.NewGRPCConnector(socket, proto.NewInputBindingClient))
}

// newGRPCInputBinding creates a new input binding for the given pluggable component.
func newGRPCInputBinding(dialer pluggable.GRPCConnectionDialer) func(l logger.Logger) bindings.InputBinding {
	return func(l logger.Logger) bindings.InputBinding {
		return inputFromConnector(l, pluggable.NewGRPCConnectorWithDialer(dialer, proto.NewInputBindingClient))
	}
}

func init() {
	//nolint:nosnakecase
	pluggable.AddServiceDiscoveryCallback(proto.InputBinding_ServiceDesc.ServiceName, func(name string, dialer pluggable.GRPCConnectionDialer) {
		DefaultRegistry.RegisterInputBinding(newGRPCInputBinding(dialer), name)
	})
}
