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

package metadata

import (
	"context"

	"google.golang.org/grpc"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/tap"
)

// Export the type.
type MD = grpcMetadata.MD

// Used as context key only
type ctxKey struct{}

// FromIncomingContext returns the incoming metadata in ctx if it exists.
func FromIncomingContext(ctx context.Context) (MD, bool) {
	md, ok := ctx.Value(ctxKey{}).(MD)
	if !ok {
		return nil, false
	}
	return md, true
}

// SetMetadataInContextUnary sets the metadata in the context for an unary gRPC invocation.
func SetMetadataInContextUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	// Because metadata.FromIncomingContext re-allocates the entire map every time to ensure the keys are lowercased, we can do it once and re-use that after
	meta, ok := grpcMetadata.FromIncomingContext(ctx)
	if ok && len(meta) > 0 {
		ctx = context.WithValue(ctx, ctxKey{}, meta)
	}
	return handler(ctx, req)
}

// SetMetadataInTapHandle sets the metadata in the context for a streaming gRPC invocation.
func SetMetadataInTapHandle(ctx context.Context, _ *tap.Info) (context.Context, error) {
	// Because metadata.FromIncomingContext re-allocates the entire map every time to ensure the keys are lowercased, we can do it once and re-use that after
	meta, ok := grpcMetadata.FromIncomingContext(ctx)
	if ok && len(meta) > 0 {
		ctx = context.WithValue(ctx, ctxKey{}, meta)
	}
	return ctx, nil
}
