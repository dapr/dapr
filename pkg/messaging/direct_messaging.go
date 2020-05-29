// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	invokeRemoteRetryCount = 3
	backoffInterval        = time.Second
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
	return d.invokeWithRetry(ctx, invokeRemoteRetryCount, backoffInterval, targetAppID, d.invokeRemote, req)
}

// invokeWithRetry will call a remote endpoint for the specified number of retries and will only retry in the case of transient failures
// TODO: check why https://github.com/grpc-ecosystem/go-grpc-middleware/blob/master/retry/examples_test.go doesn't recover the connection when target
// Server shuts down.
func (d *directMessaging) invokeWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	targetID string,
	fn func(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error),
	req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, targetID, req)
		if err == nil {
			return resp, nil
		}
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			address, adderr := d.getAddressFromMessageRequest(targetID)
			if adderr != nil {
				return nil, adderr
			}
			_, connerr := d.connectionCreatorFn(address, targetID, false, true)
			if connerr != nil {
				return nil, connerr
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

	sc := diag.FromContext(ctx)

	// TODO: Use built-in grpc client timeout instead of using context timeout
	ctx, cancel := context.WithTimeout(ctx, channel.DefaultChannelRequestTimeout)
	defer cancel()

	ctx = diag.AppendToOutgoingGRPCContext(ctx, sc)
	clientV1 := internalv1pb.NewServiceInvocationClient(conn)
	resp, err := clientV1.CallLocal(ctx, req.Proto())
	if err != nil {
		return nil, err
	}

	return invokev1.InternalInvokeResponse(resp)
}

func (d *directMessaging) getAddressFromMessageRequest(appID string) (string, error) {
	request := servicediscovery.ResolveRequest{ID: appID, Namespace: d.namespace, Port: d.grpcPort}
	return d.resolver.ResolveID(request)
}
