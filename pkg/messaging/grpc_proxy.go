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

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	grpc_proxy "github.com/dapr/dapr/pkg/grpc/proxy"
	codec "github.com/dapr/dapr/pkg/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/resiliency"

	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/proto/common/v1"
)

// Proxy is the interface for a gRPC transparent proxy.
type Proxy interface {
	Handler() grpc.StreamHandler
	SetRemoteAppFn(func(string) (remoteApp, error))
	SetTelemetryFn(func(context.Context) context.Context)
}

type proxy struct {
	appID             string
	connectionFactory messageClientConnection
	remoteAppFn       func(appID string) (remoteApp, error)
	remotePort        int
	telemetryFn       func(context.Context) context.Context
	localAppAddress   string
	acl               *config.AccessControlList
	sslEnabled        bool
	resiliency        resiliency.Provider
}

// NewProxy returns a new proxy.
func NewProxy(connectionFactory messageClientConnection, appID string, localAppAddress string, remoteDaprPort int, acl *config.AccessControlList, sslEnabled bool, resiliency resiliency.Provider) Proxy {
	return &proxy{
		appID:             appID,
		connectionFactory: connectionFactory,
		localAppAddress:   localAppAddress,
		remotePort:        remoteDaprPort,
		acl:               acl,
		sslEnabled:        sslEnabled,
		resiliency:        resiliency,
	}
}

// Handler returns a Stream Handler for handling requests that arrive for services that are not recognized by the server.
func (p *proxy) Handler() grpc.StreamHandler {
	return grpc_proxy.TransparentHandler(p.intercept, p.resiliency, p.IsLocal)
}

func (p *proxy) intercept(ctx context.Context, fullName string) (context.Context, *grpc.ClientConn, error) {
	md, _ := metadata.FromIncomingContext(ctx)

	v := md.Get(diagnostics.GRPCProxyAppIDKey)
	if len(v) == 0 {
		return ctx, nil, errors.Errorf("failed to proxy request: required metadata %s not found", diagnostics.GRPCProxyAppIDKey)
	}

	outCtx := metadata.NewOutgoingContext(ctx, md.Copy())
	appID := v[0]

	if p.remoteAppFn == nil {
		return ctx, nil, errors.Errorf("failed to proxy request: proxy not initialized. daprd startup may be incomplete.")
	}

	target, isLocal, err := p.isLocalInternal(appID)
	if err != nil {
		return ctx, nil, err
	}

	if isLocal {
		// proxy locally to the app
		if p.acl != nil {
			ok, authError := acl.ApplyAccessControlPolicies(ctx, fullName, common.HTTPExtension_NONE, config.GRPCProtocol, p.acl)
			if !ok {
				return ctx, nil, status.Errorf(codes.PermissionDenied, authError)
			}
		}

		conn, cErr := p.connectionFactory(outCtx, p.localAppAddress, p.appID, "", true, false, p.sslEnabled, grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())))
		return outCtx, conn, cErr
	}

	// proxy to a remote daprd
	// connection is recreated because its certification may have already been expired
	conn, cErr := p.connectionFactory(outCtx, target.address, target.id, target.namespace, false, true, false, grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())))
	outCtx = p.telemetryFn(outCtx)

	return outCtx, conn, cErr
}

// SetRemoteAppFn sets a function that helps the proxy resolve an app ID to an actual address.
func (p *proxy) SetRemoteAppFn(remoteAppFn func(appID string) (remoteApp, error)) {
	p.remoteAppFn = remoteAppFn
}

// SetTelemetryFn sets a function that enriches the context with telemetry.
func (p *proxy) SetTelemetryFn(spanFn func(context.Context) context.Context) {
	p.telemetryFn = spanFn
}

// Expose the functionality to detect if apps are local or not.
func (p *proxy) IsLocal(appID string) (bool, error) {
	_, isLocal, err := p.isLocalInternal(appID)
	return isLocal, err
}

func (p *proxy) isLocalInternal(appID string) (remoteApp, bool, error) {
	if p.remoteAppFn == nil {
		return remoteApp{}, false, errors.Errorf("failed to proxy request: proxy not initialized. daprd startup may be incomplete.")
	}

	target, err := p.remoteAppFn(appID)
	if err != nil {
		return remoteApp{}, false, err
	}
	return target, target.id == p.appID, nil
}
