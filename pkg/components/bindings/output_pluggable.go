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

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/components/pluggable"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"
)

// grpcOutputBinding is a implementation of a outputbinding over a gRPC Protocol.
type grpcOutputBinding struct {
	*pluggable.GRPCConnector[proto.OutputBindingClient]
	bindings.OutputBinding
	operations []bindings.OperationKind
}

// Init initializes the grpc outputbinding passing out the metadata to the grpc component.
func (b *grpcOutputBinding) Init(ctx context.Context, metadata bindings.Metadata) error {
	if err := b.Dial(metadata.Name); err != nil {
		return err
	}

	protoMetadata := &proto.MetadataRequest{
		Properties: metadata.Properties,
	}

	_, err := b.Client.Init(b.Context, &proto.OutputBindingInitRequest{
		Metadata: protoMetadata,
	})
	if err != nil {
		return err
	}

	operations, err := b.Client.ListOperations(b.Context, &proto.ListOperationsRequest{})
	if err != nil {
		return err
	}

	operationsList := operations.GetOperations()
	ops := make([]bindings.OperationKind, len(operationsList))

	for idx, op := range operationsList {
		ops[idx] = bindings.OperationKind(op)
	}
	b.operations = ops

	return nil
}

// Operations list bindings operations.
func (b *grpcOutputBinding) Operations() []bindings.OperationKind {
	return b.operations
}

// Invoke the component with the given payload, metadata and operation.
func (b *grpcOutputBinding) Invoke(ctx context.Context, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	resp, err := b.Client.Invoke(ctx, &proto.InvokeRequest{
		Data:      req.Data,
		Metadata:  req.Metadata,
		Operation: string(req.Operation),
	})
	if err != nil {
		return nil, err
	}

	var contentType *string
	if len(resp.GetContentType()) != 0 {
		contentType = &resp.ContentType
	}

	return &bindings.InvokeResponse{
		Data:        resp.GetData(),
		Metadata:    resp.GetMetadata(),
		ContentType: contentType,
	}, nil
}

// outputFromConnector creates a new GRPC outputbinding using the given underlying connector.
func outputFromConnector(_ logger.Logger, connector *pluggable.GRPCConnector[proto.OutputBindingClient]) *grpcOutputBinding {
	return &grpcOutputBinding{
		GRPCConnector: connector,
	}
}

// NewGRPCOutputBinding creates a new grpc outputbinding using the given socket factory.
func NewGRPCOutputBinding(l logger.Logger, socket string) *grpcOutputBinding {
	return outputFromConnector(l, pluggable.NewGRPCConnector(socket, proto.NewOutputBindingClient))
}

// newGRPCOutputBinding creates a new output binding for the given pluggable component.
func newGRPCOutputBinding(dialer pluggable.GRPCConnectionDialer) func(l logger.Logger) bindings.OutputBinding {
	return func(l logger.Logger) bindings.OutputBinding {
		return outputFromConnector(l, pluggable.NewGRPCConnectorWithDialer(dialer, proto.NewOutputBindingClient))
	}
}

func init() {
	//nolint:nosnakecase
	pluggable.AddServiceDiscoveryCallback(proto.OutputBinding_ServiceDesc.ServiceName, func(name string, dialer pluggable.GRPCConnectionDialer) {
		DefaultRegistry.RegisterOutputBinding(newGRPCOutputBinding(dialer), name)
	})
}
