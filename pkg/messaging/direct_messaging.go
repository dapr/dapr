// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
)

const (
	invokeRemoteRetryCount = 3
)

// messageClientConnection is the function type to connect to the other
// applications to send the message using service invocation.
type messageClientConnection func(address, id string, skipTLS, recreateIfExists bool) (*grpc.ClientConn, error)

// DirectMessaging is the API interface for invoking a remote app
type DirectMessaging interface {
	Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}

type directMessaging struct {
	appChannel          channel.AppChannel
	connectionCreatorFn messageClientConnection
	appID               string
	mode                modes.DaprMode
	grpcPort            int
	namespace           string
	resolver            servicediscovery.Resolver
	tracingSpec         config.TracingSpec
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(
	appID, namespace string,
	port int, mode modes.DaprMode,
	appChannel channel.AppChannel,
	clientConnFn messageClientConnection,
	resolver servicediscovery.Resolver,
	tracingSpec config.TracingSpec) DirectMessaging {
	return &directMessaging{
		appChannel:          appChannel,
		connectionCreatorFn: clientConnFn,
		appID:               appID,
		mode:                mode,
		grpcPort:            port,
		namespace:           namespace,
		resolver:            resolver,
		tracingSpec:         tracingSpec,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if targetAppID == d.appID {
		return d.invokeLocal(ctx, req)
	}
	return d.invokeWithRetry(ctx, invokeRemoteRetryCount, targetAppID, d.invokeRemote, req)
}

// invokeWithRetry will call a remote endpoint for the specified number of retries and will only retry in the case of transient failures
// TODO: check why https://github.com/grpc-ecosystem/go-grpc-middleware/blob/master/retry/examples_test.go doesn't recover the connection when target
// Server shuts down.
func (d *directMessaging) invokeWithRetry(
	ctx context.Context,
	numRetries int,
	targetID string,
	fn func(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error),
	req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, targetID, req)
		if err == nil {
			return resp, nil
		}

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			address, addErr := d.getAddressFromMessageRequest(targetID)
			if addErr != nil {
				return nil, addErr
			}
			_, connErr := d.connectionCreatorFn(address, targetID, false, true)
			if connErr != nil {
				return nil, connErr
			}
			continue
		}
		return resp, err
	}
	return nil, fmt.Errorf("failed to invoke target %s after %v retries", targetID, numRetries)
}

func (d *directMessaging) invokeLocal(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	return d.appChannel.InvokeMethod(ctx, req)
}

func (d *directMessaging) invokeRemote(ctx context.Context, targetID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	address, err := d.getAddressFromMessageRequest(targetID)
	if err != nil {
		return nil, err
	}

	conn, err := d.connectionCreatorFn(address, targetID, false, false)
	if err != nil {
		return nil, err
	}

	// TODO: Use built-in grpc client timeout instead of using context timeout
	ctx, cancel := context.WithTimeout(ctx, channel.DefaultChannelRequestTimeout)
	defer cancel()

	var span *trace.Span
	ctx, span = diag.StartTracingClientSpanFromGRPCContext(ctx, req.Message().Method, d.tracingSpec)
	defer span.End()

	ctx = diag.AppendToOutgoingGRPCContext(ctx, span.SpanContext())
	clientV1 := internalv1pb.NewDaprInternalClient(conn)
	resp, err := clientV1.CallLocal(ctx, req.Proto())
	if err != nil {
		return nil, err
	}

	diag.UpdateSpanPairStatusesFromError(span, err, req.Message().Method)

	return invokev1.InternalInvokeResponse(resp)
}

func (d *directMessaging) getAddressFromMessageRequest(appID string) (string, error) {
	request := servicediscovery.ResolveRequest{ID: appID, Namespace: d.namespace, Port: d.grpcPort}
	return d.resolver.ResolveID(request)
}
