// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics"
)

func connectionFn(ctx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return grpc.Dial(id, grpc.WithInsecure())
}

func TestNewProxy(t *testing.T) {
	p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
	proxy := p.(*proxy)

	assert.Equal(t, "a", proxy.appID)
	assert.Equal(t, "a:123", proxy.localAppAddress)
	assert.Equal(t, 50005, proxy.remotePort)
	assert.NotNil(t, proxy.connectionFactory)
}

func TestSetRemoteAppFn(t *testing.T) {
	p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
	p.SetRemoteAppFn(func(s string) (remoteApp, error) {
		return remoteApp{
			id: "a",
		}, nil
	})

	proxy := p.(*proxy)
	app, err := proxy.remoteAppFn("a")

	assert.NoError(t, err)
	assert.Equal(t, "a", app.id)
}

func TestSetTelemetryFn(t *testing.T) {
	p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
	p.SetTelemetryFn(func(ctx context.Context) context.Context {
		return ctx
	})

	proxy := p.(*proxy)
	ctx := metadata.NewOutgoingContext(context.TODO(), metadata.MD{"a": []string{"b"}})
	ctx = proxy.telemetryFn(ctx)

	md, _ := metadata.FromOutgoingContext(ctx)
	assert.Equal(t, "b", md["a"][0])
}

func TestHandler(t *testing.T) {
	p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
	h := p.Handler()

	assert.NotNil(t, h)
}

func TestIntercept(t *testing.T) {
	t.Run("no app-id in metadata", func(t *testing.T) {
		p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			return ctx
		})

		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id: "a",
			}, nil
		})

		ctx := metadata.NewOutgoingContext(context.TODO(), metadata.MD{"a": []string{"b"}})
		proxy := p.(*proxy)
		_, conn, err := proxy.intercept(ctx, "/test")

		assert.Error(t, err)
		assert.Nil(t, conn)
	})

	t.Run("app-id exists in metadata", func(t *testing.T) {
		p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			return ctx
		})

		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id: "a",
			}, nil
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"b"}})
		proxy := p.(*proxy)
		_, _, err := proxy.intercept(ctx, "/test")

		assert.NoError(t, err)
	})

	t.Run("proxy to the app", func(t *testing.T) {
		p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			return ctx
		})

		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id: "a",
			}, nil
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"a"}})
		proxy := p.(*proxy)
		_, conn, err := proxy.intercept(ctx, "/test")

		assert.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, "a", conn.Target())
	})

	t.Run("proxy to a remote app", func(t *testing.T) {
		p := NewProxy(connectionFn, "a", "a:123", 50005, nil)
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			ctx = metadata.AppendToOutgoingContext(ctx, "a", "b")
			return ctx
		})

		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id: "b",
			}, nil
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"b"}})
		proxy := p.(*proxy)
		ctx, conn, err := proxy.intercept(ctx, "/test")

		assert.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, "b", conn.Target())

		md, _ := metadata.FromOutgoingContext(ctx)
		assert.Equal(t, "b", md["a"][0])
	})

	t.Run("access policies applied", func(t *testing.T) {
		acl := &config.AccessControlList{
			DefaultAction: "deny",
			TrustDomain:   "public",
		}

		p := NewProxy(connectionFn, "a", "a:123", 50005, acl)
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			ctx = metadata.AppendToOutgoingContext(ctx, "a", "b")
			return ctx
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"a"}})
		proxy := p.(*proxy)

		_, conn, err := proxy.intercept(ctx, "/test")

		assert.Error(t, err)
		assert.Nil(t, conn)
	})
}
