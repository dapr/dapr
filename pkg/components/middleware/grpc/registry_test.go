/*
Copyright 2021 The Dapr Authors
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

package grpc_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"

	h "github.com/dapr/components-contrib/middleware"

	grpc_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/grpc"
	grpc "github.com/dapr/dapr/pkg/middleware/grpc"
)

func TestRegistry(t *testing.T) {
	testRegistry := grpc_middleware_loader.NewRegistry()

	t.Run("grpc unary middleware is registered", func(t *testing.T) {
		const (
			middlewareName   = "mockMiddleware"
			middlewareNameV2 = "mockMiddleware/v2"
			componentName    = "middleware.grpc.server.unary." + middlewareName
		)

		// Initiate mock object
		mock := grpc.UnaryServerMiddleware(func(ctx context.Context, req interface{}, info *grpc_go.UnaryServerInfo, handler grpc_go.UnaryHandler) (resp interface{}, err error) {
			return nil, nil
		})
		mockV2 := grpc.UnaryServerMiddleware(func(ctx context.Context, req interface{}, info *grpc_go.UnaryServerInfo, handler grpc_go.UnaryHandler) (resp interface{}, err error) {
			return nil, nil
		})
		metadata := h.Metadata{}

		// act
		testRegistry.RegisterUnaryServer(
			grpc_middleware_loader.NewUnaryServerMiddleware(middlewareName, func(metadata h.Metadata) (grpc.UnaryServerMiddleware, error) {
				return mock, nil
			}))
		testRegistry.RegisterUnaryServer(
			grpc_middleware_loader.NewUnaryServerMiddleware(middlewareNameV2, func(metadata h.Metadata) (grpc.UnaryServerMiddleware, error) {
				return mockV2, nil
			}))

		// Function values are not comparable.
		// You can't take the address of a function, but if you print it with
		// the fmt package, it prints its address. So you can use fmt.Sprintf()
		// to get the address of a function value.

		// assert v0 and v1
		p, e := testRegistry.CreateUnaryServer(componentName, "v0", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mock), fmt.Sprintf("%v", p))
		p, e = testRegistry.CreateUnaryServer(componentName, "v1", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mock), fmt.Sprintf("%v", p))

		// assert v2
		pV2, e := testRegistry.CreateUnaryServer(componentName, "v2", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mockV2), fmt.Sprintf("%v", pV2))

		// check case-insensitivity
		pV2, e = testRegistry.CreateUnaryServer(strings.ToUpper(componentName), "V2", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mockV2), fmt.Sprintf("%v", pV2))
	})

	t.Run("middleware is not registered", func(t *testing.T) {
		const (
			middlewareName   = "fakeGRPCMiddleware"
			componentName    = grpc_middleware_loader.UnaryServerMiddlewareTypePrefix + middlewareName
			componentVersion = "v1"
		)

		metadata := h.Metadata{}

		// act
		p, err := testRegistry.CreateUnaryServer(componentName, componentVersion, metadata)

		// assert
		assert.Nil(t, p)
		assert.ErrorIs(t, err, &grpc_middleware_loader.ErrUnaryServerNotRegistered{})
	})
}
