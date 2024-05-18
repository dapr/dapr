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

package messaging

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

func connectionFn(ctx context.Context, address, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(bool), error) {
	conn, err := grpc.Dial(id,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			// Don't actually connect to anything
			return &net.TCPConn{}, nil
		}),
	)
	if err != nil {
		return nil, func(bool) {}, err
	}
	teardown := func(bool) { conn.Close() }
	return conn, teardown, err
}

func appClientFn() (grpc.ClientConnInterface, error) {
	appClient, _, err := connectionFn(context.Background(), "a:123", "a", "")
	return appClient, err
}

func TestNewProxy(t *testing.T) {
	p := NewProxy(ProxyOpts{
		ConnectionFactory: connectionFn,
		AppClientFn:       appClientFn,
		AppID:             "a",
		ACL:               nil,
		Resiliency:        resiliency.New(nil),
	})
	proxy := p.(*proxy)

	assert.Equal(t, "a", proxy.appID)
	assert.NotNil(t, proxy.connectionFactory)
	assert.Equal(t, reflect.ValueOf(connectionFn).Pointer(), reflect.ValueOf(proxy.connectionFactory).Pointer())
}

func TestSetRemoteAppFn(t *testing.T) {
	p := NewProxy(ProxyOpts{
		ConnectionFactory: connectionFn,
		AppClientFn:       appClientFn,
		AppID:             "a",
		ACL:               nil,
		Resiliency:        resiliency.New(nil),
	})
	p.SetRemoteAppFn(func(s string) (remoteApp, error) {
		return remoteApp{
			id: "a",
		}, nil
	})

	proxy := p.(*proxy)
	app, err := proxy.remoteAppFn("a")

	require.NoError(t, err)
	assert.Equal(t, "a", app.id)
}

func TestSetTelemetryFn(t *testing.T) {
	p := NewProxy(ProxyOpts{
		ConnectionFactory: connectionFn,
		AppClientFn:       appClientFn,
		AppID:             "a",
		ACL:               nil,
		Resiliency:        resiliency.New(nil),
	})
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
	p := NewProxy(ProxyOpts{
		ConnectionFactory: connectionFn,
		AppClientFn:       appClientFn,
		AppID:             "a",
		Resiliency:        resiliency.New(nil),
	})
	h := p.Handler()

	assert.NotNil(t, h)
}

func TestIntercept(t *testing.T) {
	t.Run("no app-id in metadata", func(t *testing.T) {
		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			Resiliency:        resiliency.New(nil),
		})
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
		_, conn, _, teardown, err := proxy.intercept(ctx, "/test")
		defer teardown(true)

		require.Error(t, err)
		assert.Nil(t, conn)
	})

	t.Run("app-id exists in metadata", func(t *testing.T) {
		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			Resiliency:        resiliency.New(nil),
		})
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
		_, _, _, _, err := proxy.intercept(ctx, "/test")

		require.NoError(t, err)
	})

	t.Run("proxy to the app", func(t *testing.T) {
		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			Resiliency:        resiliency.New(nil),
		})
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
		_, conn, _, teardown, err := proxy.intercept(ctx, "/test")
		defer teardown(true)

		require.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, "a", conn.Target())
	})

	t.Run("proxy to a remote app", func(t *testing.T) {
		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			Resiliency:        resiliency.New(nil),
		})
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			ctx = metadata.AppendToOutgoingContext(ctx, "a", "b")
			return ctx
		})

		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id: "b",
			}, nil
		})

		t.Setenv(securityConsts.AppAPITokenEnvVar, "token1")

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"b"}})
		proxy := p.(*proxy)
		ctx, conn, _, teardown, err := proxy.intercept(ctx, "/test")
		defer teardown(true)

		require.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, "b", conn.Target())

		md, _ := metadata.FromOutgoingContext(ctx)
		assert.Equal(t, "b", md["a"][0])
		assert.Equal(t, "a", md[invokev1.CallerIDHeader][0])
		assert.Equal(t, "b", md[invokev1.CalleeIDHeader][0])
		assert.Equal(t, "token1", md[securityConsts.APITokenHeader][0])
	})

	t.Run("access policies applied", func(t *testing.T) {
		acl := &config.AccessControlList{
			DefaultAction: "deny",
			TrustDomain:   "public",
		}

		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			ACL:               acl,
			Resiliency:        resiliency.New(nil),
		})
		p.SetRemoteAppFn(func(s string) (remoteApp, error) {
			return remoteApp{
				id:      "a",
				address: "a:123",
			}, nil
		})
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			ctx = metadata.AppendToOutgoingContext(ctx, "a", "b")
			return ctx
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"a"}})
		proxy := p.(*proxy)

		_, conn, _, teardown, err := proxy.intercept(ctx, "/test")
		defer teardown(true)

		require.Error(t, err)
		assert.Nil(t, conn)
	})

	t.Run("SetRemoteAppFn never called", func(t *testing.T) {
		p := NewProxy(ProxyOpts{
			ConnectionFactory: connectionFn,
			AppClientFn:       appClientFn,
			AppID:             "a",
			Resiliency:        resiliency.New(nil),
		})
		p.SetTelemetryFn(func(ctx context.Context) context.Context {
			return ctx
		})

		ctx := metadata.NewIncomingContext(context.TODO(), metadata.MD{diagnostics.GRPCProxyAppIDKey: []string{"a"}})
		proxy := p.(*proxy)
		_, conn, _, teardown, err := proxy.intercept(ctx, "/test")
		defer teardown(true)

		require.Error(t, err)
		assert.Nil(t, conn)
	})
}
