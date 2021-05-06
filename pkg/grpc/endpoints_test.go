// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestSetAPIEndpointsMiddlewareUnary(t *testing.T) {
	t.Run("state endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "state",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["state.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("publish endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "publish",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["publish.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("actors endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "actors",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["actors.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("bindings endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "bindings",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["bindings.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("secrets endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "secrets",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["secrets.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("metadata endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "metadata",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["metadata.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("shutdown endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "shutdown",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["shutdown.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("invoke endpoints disallowed", func(t *testing.T) {
		a := []config.APIAccessRule{
			{
				Name:    "invoke",
				Version: "v1",
			},
		}

		f := setAPIEndpointsMiddlewareUnary(a)

		for _, e := range endpoints["invoke.v1"] {
			_, err := f(nil, nil, &grpc.UnaryServerInfo{
				FullMethod: e,
			}, nil)
			assert.Error(t, err)
		}
	})

	t.Run("no rules, call allowed", func(t *testing.T) {
		h := func(ctx context.Context, req interface{}) (interface{}, error) {
			return nil, nil
		}
		f := setAPIEndpointsMiddlewareUnary(nil)

		_, err := f(nil, nil, &grpc.UnaryServerInfo{
			FullMethod: "v1.method",
		}, h)
		assert.NoError(t, err)
	})
}
