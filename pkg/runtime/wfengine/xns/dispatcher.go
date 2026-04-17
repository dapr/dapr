/*
Copyright 2026 The Dapr Authors
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

// Package xns implements the concrete XNSDispatcher used by the workflow
// orchestrator actor to perform the cross-namespace service-invocation hop.
//
// The dispatcher owns the two things the actor deliberately does not pull in
// directly: name resolution (turning an appID.namespace tuple into a sidecar
// address) and the gRPC manager (establishing a cross-trust-domain mTLS
// connection). Keeping these here lets the orchestrator package stay
// mock-friendly in unit tests, while the runtime wires a real instance in
// at startup.
package xns

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nr "github.com/dapr/components-contrib/nameresolution"
	orchestrator "github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Options configures a Dispatcher.
type Options struct {
	// Resolver is the name-resolution component for turning (appID, namespace)
	// into a set of sidecar addresses. When nil, DispatchWorkflow and
	// DeliverResult return Unavailable.
	Resolver nr.Resolver

	// ResolverMulti is the optional multi-resolver interface. When set, used
	// to pick one of several addresses returned by the resolver; otherwise
	// falls back to the single-address Resolver.
	ResolverMulti nr.ResolverMulti

	// GetGRPCConnection returns a connection to the target sidecar with the
	// correct cross-trust-domain mTLS dial options. This mirrors
	// grpc.Manager.GetGRPCConnection so the runtime can pass the manager's
	// method directly without introducing an import cycle.
	GetGRPCConnection func(ctx context.Context, address, id, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

	// InternalGRPCPort is the daprd-to-daprd port used when the resolver
	// doesn't return a port-qualified address.
	InternalGRPCPort int
}

// Dispatcher implements orchestrator.XNSDispatcher.
type Dispatcher struct {
	resolver      nr.Resolver
	resolverMulti nr.ResolverMulti
	dial          func(ctx context.Context, address, id, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)
	port          int
}

// New returns a Dispatcher. The returned value satisfies
// orchestrator.XNSDispatcher.
func New(opts Options) *Dispatcher {
	return &Dispatcher{
		resolver:      opts.Resolver,
		resolverMulti: opts.ResolverMulti,
		dial:          opts.GetGRPCConnection,
		port:          opts.InternalGRPCPort,
	}
}

var _ orchestrator.XNSDispatcher = (*Dispatcher)(nil)

// DispatchWorkflow resolves the target sidecar, dials with mTLS, and invokes
// CallWorkflowCrossNamespace. Errors are returned unwrapped (as gRPC status
// errors) so the caller-side reminder handler can classify
// PermissionDenied / Unimplemented / AlreadyExists terminally.
func (d *Dispatcher) DispatchWorkflow(ctx context.Context, targetNamespace, targetAppID string, req *internalv1pb.CrossNSDispatchRequest) error {
	conn, done, err := d.connect(ctx, targetNamespace, targetAppID)
	if err != nil {
		return err
	}
	defer done(false)

	client := internalv1pb.NewServiceInvocationClient(conn)
	_, err = client.CallWorkflowCrossNamespace(ctx, req)
	return err
}

// DeliverResult resolves the parent's sidecar and invokes
// DeliverWorkflowResultCrossNamespace.
func (d *Dispatcher) DeliverResult(ctx context.Context, parentNamespace, parentAppID string, req *internalv1pb.CrossNSResultRequest) error {
	conn, done, err := d.connect(ctx, parentNamespace, parentAppID)
	if err != nil {
		return err
	}
	defer done(false)

	client := internalv1pb.NewServiceInvocationClient(conn)
	_, err = client.DeliverWorkflowResultCrossNamespace(ctx, req)
	return err
}

// connect resolves the target sidecar's address and returns a gRPC client
// connection with the proper cross-trust-domain mTLS options.
func (d *Dispatcher) connect(ctx context.Context, namespace, appID string) (*grpc.ClientConn, func(destroy bool), error) {
	if d.resolver == nil {
		return nil, nil, status.Error(codes.Unavailable, "cross-namespace dispatcher: name resolver not configured")
	}
	if d.dial == nil {
		return nil, nil, status.Error(codes.Unavailable, "cross-namespace dispatcher: gRPC connection factory not configured")
	}
	if namespace == "" || appID == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "cross-namespace dispatcher: empty namespace or appID")
	}

	address, err := d.resolve(ctx, namespace, appID)
	if err != nil {
		return nil, nil, err
	}
	if address == "" {
		return nil, nil, status.Errorf(codes.Unavailable, "cross-namespace dispatcher: empty address for '%s/%s'", namespace, appID)
	}

	conn, done, cerr := d.dial(ctx, address, appID, namespace)
	if cerr != nil {
		// Preserve the code if the connection factory already returned a
		// gRPC status; otherwise wrap as Unavailable so caller retries.
		if _, ok := status.FromError(cerr); ok {
			return nil, nil, cerr
		}
		return nil, nil, status.Errorf(codes.Unavailable, "cross-namespace dispatcher: dial '%s/%s' at %s: %v", namespace, appID, address, cerr)
	}
	return conn, done, nil
}

func (d *Dispatcher) resolve(ctx context.Context, namespace, appID string) (string, error) {
	req := nr.ResolveRequest{
		ID:        appID,
		Namespace: namespace,
		Port:      d.port,
	}

	if d.resolverMulti != nil {
		addrs, err := d.resolverMulti.ResolveIDMulti(ctx, req)
		if err != nil {
			return "", status.Errorf(codes.Unavailable, "cross-namespace dispatcher: resolve '%s/%s': %v", namespace, appID, err)
		}
		addr := addrs.Pick()
		if addr == "" {
			return "", status.Errorf(codes.Unavailable, "cross-namespace dispatcher: no addresses for '%s/%s'", namespace, appID)
		}
		return addr, nil
	}

	addr, err := d.resolver.ResolveID(ctx, req)
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "cross-namespace dispatcher: resolve '%s/%s': %v", namespace, appID, err)
	}
	return addr, nil
}

// ErrResolverMissing is returned by New when constructed without a resolver.
// Callers typically map this to a fatal startup error when the feature is
// enabled but no name-resolution component is available.
var ErrResolverMissing = errors.New("cross-namespace dispatcher: resolver is required")

// Validate reports whether the Options are sufficient to produce a working
// Dispatcher. Use at startup to fail fast rather than at first reminder fire.
func (o Options) Validate() error {
	if o.Resolver == nil {
		return ErrResolverMissing
	}
	if o.GetGRPCConnection == nil {
		return errors.New("cross-namespace dispatcher: GetGRPCConnection is required")
	}
	return nil
}
