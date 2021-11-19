// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	h "github.com/dapr/components-contrib/middleware"

	"github.com/dapr/dapr/pkg/components/middleware/http"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
)

func TestRegistry(t *testing.T) {
	testRegistry := http.NewRegistry()

	t.Run("middleware is registered", func(t *testing.T) {
		const (
			middlewareName   = "mockMiddleware"
			middlewareNameV2 = "mockMiddleware/v2"
			componentName    = "middleware.http." + middlewareName
		)

		// Initiate mock object
		mock := http_middleware.Middleware(func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return nil
		})
		mockV2 := http_middleware.Middleware(func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
			return nil
		})
		metadata := h.Metadata{}

		// act
		testRegistry.Register(http.New(middlewareName, func(h.Metadata) (http_middleware.Middleware, error) {
			return mock, nil
		}))
		testRegistry.Register(http.New(middlewareNameV2, func(h.Metadata) (http_middleware.Middleware, error) {
			return mockV2, nil
		}))

		// Function values are not comparable.
		// You can't take the address of a function, but if you print it with
		// the fmt package, it prints its address. So you can use fmt.Sprintf()
		// to get the address of a function value.

		// assert v0 and v1
		p, e := testRegistry.Create(componentName, "v0", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mock), fmt.Sprintf("%v", p))
		p, e = testRegistry.Create(componentName, "v1", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mock), fmt.Sprintf("%v", p))

		// assert v2
		pV2, e := testRegistry.Create(componentName, "v2", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mockV2), fmt.Sprintf("%v", pV2))

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(componentName), "V2", metadata)
		assert.NoError(t, e)
		assert.Equal(t, fmt.Sprintf("%v", mockV2), fmt.Sprintf("%v", pV2))
	})

	t.Run("middleware is not registered", func(t *testing.T) {
		const (
			middlewareName = "fakeMiddleware"
			componentName  = "middleware.http." + middlewareName
		)

		metadata := h.Metadata{}

		// act
		p, actualError := testRegistry.Create(componentName, "v1", metadata)
		expectedError := errors.Errorf("HTTP middleware %s/v1 has not been registered", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
