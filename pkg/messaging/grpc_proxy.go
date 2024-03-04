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
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/acl"
	grpcProxy "github.com/dapr/dapr/pkg/api/grpc/proxy"
	codec "github.com/dapr/dapr/pkg/api/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

// Proxy is the interface for a gRPC transparent proxy.
type Proxy interface {
	Handler() grpc.StreamHandler
	SetRemoteAppFn(func(string) (remoteApp, error))
	SetTelemetryFn(func(context.Context) context.Context)
}

type proxy struct {
	appID              string
	appClientFn        func() (grpc.ClientConnInterface, error)
	connectionFactory  messageClientConnection
	remoteAppFn        func(appID string) (remoteApp, error)
	telemetryFn        func(context.Context) context.Context
	acl                *config.AccessControlList
	resiliency         resiliency.Provider
	maxRequestBodySize int
}

// ProxyOpts is the struct with options for NewProxy.
type ProxyOpts struct {
	AppClientFn        func() (grpc.ClientConnInterface, error)
	ConnectionFactory  messageClientConnection
	AppID              string
	ACL                *config.AccessControlList
	Resiliency         resiliency.Provider
	MaxRequestBodySize int
}

// NewProxy returns a new proxy.
func NewProxy(opts ProxyOpts) Proxy {
	return &proxy{
		appClientFn:        opts.AppClientFn,
		appID:              opts.AppID,
		connectionFactory:  opts.ConnectionFactory,
		acl:                opts.ACL,
		resiliency:         opts.Resiliency,
		maxRequestBodySize: opts.MaxRequestBodySize,
	}
}

// Handler returns a Stream Handler for handling requests that arrive for services that are not recognized by the server.
func (p *proxy) Handler() grpc.StreamHandler {
	return grpcProxy.TransparentHandler(p.intercept,
		func(appID, methodName string) *resiliency.PolicyDefinition {
			_, isLocal, err := p.isLocal(appID)
			if err == nil && !isLocal {
				return p.resiliency.EndpointPolicy(appID, appID+":"+methodName)
			}

			return resiliency.NoOp{}.EndpointPolicy("", "")
		},
		grpcProxy.DirectorConnectionFactory(p.connectionFactory),
		p.maxRequestBodySize,
	)
}

func nopTeardown(destroy bool) {
	// Nop
}

func (p *proxy) intercept(ctx context.Context, fullName string) (context.Context, *grpc.ClientConn, *grpcProxy.ProxyTarget, func(destroy bool), error) {
	md, _ := metadata.FromIncomingContext(ctx)

	v := md[diagnostics.GRPCProxyAppIDKey]
	if len(v) == 0 {
		return ctx, nil, nil, nopTeardown, fmt.Errorf("failed to proxy request: required metadata %s not found", diagnostics.GRPCProxyAppIDKey)
	}

	outCtx := metadata.NewOutgoingContext(ctx, md.Copy())
	appID := v[0]

	if p.remoteAppFn == nil {
		return ctx, nil, nil, nopTeardown, errors.New("failed to proxy request: proxy not initialized. daprd startup may be incomplete")
	}

	target, isLocal, err := p.isLocal(appID)
	if err != nil {
		return ctx, nil, nil, nopTeardown, err
	}

	if isLocal {
		// proxy locally to the app
		if p.acl != nil {
			ok, authError := acl.ApplyAccessControlPolicies(ctx, fullName, common.HTTPExtension_NONE, false, p.acl) //nolint:nosnakecase
			if !ok {
				return ctx, nil, nil, nopTeardown, status.Errorf(codes.PermissionDenied, authError)
			}
		}

		var appClient grpc.ClientConnInterface
		appClient, err = p.appClientFn()
		if err != nil {
			return ctx, nil, nil, nopTeardown, err
		}
		return outCtx, appClient.(*grpc.ClientConn), nil, nopTeardown, nil
	}

	// proxy to a remote daprd
	conn, teardown, cErr := p.connectionFactory(outCtx, target.address, target.id, target.namespace,
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	outCtx = p.telemetryFn(outCtx)
	outCtx = metadata.AppendToOutgoingContext(outCtx, invokev1.CallerIDHeader, p.appID, invokev1.CalleeIDHeader, target.id)

	appMetadataToken := security.GetAppToken()
	if appMetadataToken != "" {
		outCtx = metadata.AppendToOutgoingContext(outCtx, securityConsts.APITokenHeader, appMetadataToken)
	}

	pt := &grpcProxy.ProxyTarget{
		ID:        target.id,
		Namespace: target.namespace,
		Address:   target.address,
	}

	return outCtx, conn, pt, teardown, cErr
}

// SetRemoteAppFn sets a function that helps the proxy resolve an app ID to an actual address.
func (p *proxy) SetRemoteAppFn(remoteAppFn func(appID string) (remoteApp, error)) {
	p.remoteAppFn = remoteAppFn
}

// SetTelemetryFn sets a function that enriches the context with telemetry.
func (p *proxy) SetTelemetryFn(spanFn func(context.Context) context.Context) {
	p.telemetryFn = spanFn
}

func (p *proxy) isLocal(appID string) (remoteApp, bool, error) {
	if p.remoteAppFn == nil {
		return remoteApp{}, false, errors.New("failed to proxy request: proxy not initialized; daprd startup may be incomplete")
	}

	target, err := p.remoteAppFn(appID)
	if err != nil {
		return remoteApp{}, false, err
	}
	return target, target.id == p.appID, nil
}
