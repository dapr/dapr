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
	"github.com/dapr/dapr/pkg/api/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/messaging/method"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

// Proxy is the interface for a gRPC transparent proxy.
type Proxy interface {
	// Handler returns the stream handler for the public API server, which only
	// the local app reaches.
	Handler() grpc.StreamHandler
	// InternalHandler returns the stream handler for the internal server, which
	// other daprds reach.
	InternalHandler() grpc.StreamHandler
	SetRemoteAppFn(func(context.Context, string) (remoteApp, error))
	SetTelemetryFn(func(context.Context) context.Context)
}

type proxy struct {
	appID              string
	namespace          string
	appClientFn        func() (grpc.ClientConnInterface, func(bool), error)
	connectionFactory  messageClientConnection
	remoteAppFn        func(ctx context.Context, appID string) (remoteApp, error)
	telemetryFn        func(context.Context) context.Context
	appendAppTokenFn   func(context.Context) context.Context
	acl                *config.AccessControlList
	resiliency         resiliency.Provider
	maxRequestBodySize int
}

// ProxyOpts is the struct with options for NewProxy.
type ProxyOpts struct {
	AppClientFn        func() (grpc.ClientConnInterface, func(bool), error)
	ConnectionFactory  messageClientConnection
	AppID              string
	Namespace          string
	ACL                *config.AccessControlList
	Resiliency         resiliency.Provider
	MaxRequestBodySize int
	AppendAppTokenFn   func(context.Context) context.Context
}

// NewProxy returns a new proxy.
func NewProxy(opts ProxyOpts) Proxy {
	return &proxy{
		appClientFn:        opts.AppClientFn,
		appID:              opts.AppID,
		namespace:          opts.Namespace,
		connectionFactory:  opts.ConnectionFactory,
		appendAppTokenFn:   opts.AppendAppTokenFn,
		acl:                opts.ACL,
		resiliency:         opts.Resiliency,
		maxRequestBodySize: opts.MaxRequestBodySize,
	}
}

func (p *proxy) Handler() grpc.StreamHandler {
	return p.handler(true)
}

func (p *proxy) InternalHandler() grpc.StreamHandler {
	return p.handler(false)
}

// handler returns a Stream Handler for handling requests that arrive for
// services that are not recognized by the server. fromApp is true when the
// server only receives requests from the local app.
func (p *proxy) handler(fromApp bool) grpc.StreamHandler {
	return grpcProxy.TransparentHandler(func(ctx context.Context, fullName string) (context.Context, *grpc.ClientConn, *grpcProxy.ProxyTarget, func(destroy bool), error) {
		return p.intercept(ctx, fullName, fromApp)
	},
		func(ctx context.Context, appID, methodName string) *resiliency.PolicyDefinition {
			_, isLocal, err := p.isLocal(ctx, appID)
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

func (p *proxy) intercept(ctx context.Context, fullName string, fromApp bool) (context.Context, *grpc.ClientConn, *grpcProxy.ProxyTarget, func(destroy bool), error) {
	md, _ := metadata.FromIncomingContext(ctx)

	v := md[diagConsts.GRPCProxyCalleeIDKey]
	if len(v) == 0 {
		log.Debugf("failed to proxy request: required metadata %s not found, fallback to %s", diagConsts.GRPCProxyCalleeIDKey, diagConsts.GRPCProxyAppIDKey)
		v = md[diagConsts.GRPCProxyAppIDKey]
		if len(v) == 0 {
			return ctx, nil, nil, nopTeardown, fmt.Errorf("failed to proxy request: required metadata %s or %s not found", diagConsts.GRPCProxyCalleeIDKey, diagConsts.GRPCProxyAppIDKey)
		}
	}

	appID := v[0]

	if p.remoteAppFn == nil {
		return ctx, nil, nil, nopTeardown, errors.New("failed to proxy request: proxy not initialized. daprd startup may be incomplete")
	}

	target, isLocal, err := p.isLocal(ctx, appID)
	if err != nil {
		return ctx, nil, nil, nopTeardown, err
	}

	if isLocal {
		// Normalize the method before ACL evaluation and dispatch.
		normalizedName, normErr := method.NormalizeMethod(fullName)
		if normErr != nil {
			return ctx, nil, nil, nopTeardown, status.Errorf(codes.InvalidArgument, "invalid method: %v", normErr)
		}
		fullName = normalizedName

		// proxy locally to the app
		if p.acl != nil {
			ok, authError := acl.ApplyAccessControlPolicies(ctx, fullName, common.HTTPExtension_NONE, false, p.acl) //nolint:nosnakecase
			if !ok {
				return ctx, nil, nil, nopTeardown, status.Error(codes.PermissionDenied, authError)
			}
		}

		mdCopy := md.Copy()
		delete(mdCopy, securityConsts.APITokenHeader)
		// A self-invocation by the local app: caller == callee == this app.
		callerAppID, callerNamespace := p.appID, p.namespace
		if !fromApp {
			id, _, idErr := spiffe.FromGRPCContext(ctx)
			if idErr != nil {
				return ctx, nil, nil, nopTeardown, status.Error(codes.PermissionDenied, idErr.Error())
			}
			callerAppID, callerNamespace = "", ""
			if id != nil {
				callerAppID, callerNamespace = id.AppID(), id.Namespace()
			}
		}
		setLocalIdentityMetadata(mdCopy, callerAppID, callerNamespace, p.appID)

		var appClient grpc.ClientConnInterface
		var teardown func(bool)
		appClient, teardown, err = p.appClientFn()
		if err != nil {
			return ctx, nil, nil, nopTeardown, err
		}

		outCtx := metadata.NewOutgoingContext(ctx, mdCopy)
		if p.appendAppTokenFn != nil {
			outCtx = p.appendAppTokenFn(outCtx)
		}
		return outCtx, appClient.(*grpc.ClientConn), nil, teardown, nil
	}

	// Drop any trace headers the caller already set (e.g. a gRPC client
	// whose HTTP transport auto-instruments its own traceparent) before
	// forwarding to the remote daprd. telemetryFn below appends this
	// sidecar's own span context as the trace header for the next hop;
	// leaving the caller's raw header in place would result in two
	// comma-joined values for the same metadata key on the wire.
	mdCopy := md.Copy()
	delete(mdCopy, diagConsts.TraceparentHeader)
	delete(mdCopy, diagConsts.TracestateHeader)
	delete(mdCopy, diagConsts.GRPCTraceContextKey)
	mdCopy.Set(invokev1.CallerIDHeader, p.appID)
	mdCopy.Set(invokev1.CallerNamespaceHeader, p.namespace)
	mdCopy.Set(invokev1.CalleeIDHeader, target.id)
	outCtx := metadata.NewOutgoingContext(ctx, mdCopy)

	// proxy to a remote daprd
	conn, teardown, cErr := p.connectionFactory(outCtx, target.address, target.id, target.namespace,
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	outCtx = p.telemetryFn(outCtx)

	pt := &grpcProxy.ProxyTarget{
		ID:        target.id,
		Namespace: target.namespace,
		Address:   target.address,
	}

	return outCtx, conn, pt, teardown, cErr
}

// setLocalIdentityMetadata replaces the caller/callee identity metadata of a
// request proxied to the local app. With a known caller (the local app itself,
// or the peer's SPIFFE ID with mTLS), the identity is stamped and the values
// an older caller daprd forwarded from its app are discarded. Without mTLS
// there is no peer identity to check against: an older caller daprd appends
// its own values after those its app sent, so only the last value is kept.
func setLocalIdentityMetadata(md metadata.MD, callerAppID, callerNamespace, appID string) {
	if callerAppID != "" {
		md.Set(invokev1.CallerIDHeader, callerAppID)
		md.Set(invokev1.CallerNamespaceHeader, callerNamespace)
		md.Set(invokev1.CalleeIDHeader, appID)
		return
	}
	for _, k := range []string{invokev1.CallerIDHeader, invokev1.CallerNamespaceHeader, invokev1.CalleeIDHeader} {
		if v := md[k]; len(v) > 1 {
			md[k] = v[len(v)-1:]
		}
	}
}

// SetRemoteAppFn sets a function that helps the proxy resolve an app ID to an actual address.
func (p *proxy) SetRemoteAppFn(remoteAppFn func(ctx context.Context, appID string) (remoteApp, error)) {
	p.remoteAppFn = remoteAppFn
}

// SetTelemetryFn sets a function that enriches the context with telemetry.
func (p *proxy) SetTelemetryFn(spanFn func(context.Context) context.Context) {
	p.telemetryFn = spanFn
}

func (p *proxy) isLocal(ctx context.Context, appID string) (remoteApp, bool, error) {
	if p.remoteAppFn == nil {
		return remoteApp{}, false, errors.New("failed to proxy request: proxy not initialized; daprd startup may be incomplete")
	}

	target, err := p.remoteAppFn(ctx, appID)
	if err != nil {
		return remoteApp{}, false, err
	}
	return target, target.id == p.appID, nil
}
