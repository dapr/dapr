// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"os"
	"strings"
	"time"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/utils"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// messageClientConnection is the function type to connect to the other
// applications to send the message using service invocation.
type messageClientConnection func(address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool) (*grpc.ClientConn, error)

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
	resolver            nr.Resolver
	tracingSpec         config.TracingSpec
	hostAddress         string
	hostName            string
}

type remoteApp struct {
	id        string
	namespace string
	address   string
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(
	appID, namespace string,
	port int, mode modes.DaprMode,
	appChannel channel.AppChannel,
	clientConnFn messageClientConnection,
	resolver nr.Resolver,
	tracingSpec config.TracingSpec) DirectMessaging {
	hAddr, _ := utils.GetHostAddress()
	hName, _ := os.Hostname()
	return &directMessaging{
		appChannel:          appChannel,
		connectionCreatorFn: clientConnFn,
		appID:               appID,
		mode:                mode,
		grpcPort:            port,
		namespace:           namespace,
		resolver:            resolver,
		tracingSpec:         tracingSpec,
		hostAddress:         hAddr,
		hostName:            hName,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	app, err := d.getRemoteApp(targetAppID)
	if err != nil {
		return nil, err
	}

	if app.id == d.appID && app.namespace == d.namespace {
		return d.invokeLocal(ctx, req)
	}
	return d.invokeWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, app, d.invokeRemote, req)
}

// requestAppIDAndNamespace takes an app id and returns the app id, namespace and error.
func (d *directMessaging) requestAppIDAndNamespace(targetAppID string) (string, string, error) {
	items := strings.Split(targetAppID, ".")
	if len(items) == 1 {
		return targetAppID, d.namespace, nil
	} else if len(items) == 2 {
		return items[0], items[1], nil
	} else {
		return "", "", errors.Errorf("invalid app id %s", targetAppID)
	}
}

// invokeWithRetry will call a remote endpoint for the specified number of retries and will only retry in the case of transient failures
// TODO: check why https://github.com/grpc-ecosystem/go-grpc-middleware/blob/master/retry/examples_test.go doesn't recover the connection when target
// Server shuts down.
func (d *directMessaging) invokeWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	app remoteApp,
	fn func(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error),
	req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, app.id, app.namespace, app.address, req)
		if err == nil {
			return resp, nil
		}
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			_, connerr := d.connectionCreatorFn(app.address, app.id, app.namespace, false, true, false)
			if connerr != nil {
				return nil, connerr
			}
			continue
		}
		return resp, err
	}
	return nil, errors.Errorf("failed to invoke target %s after %v retries", app.id, numRetries)
}

func (d *directMessaging) invokeLocal(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	return d.appChannel.InvokeMethod(ctx, req)
}

func (d *directMessaging) invokeRemote(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	conn, err := d.connectionCreatorFn(appAddress, appID, namespace, false, false, false)
	if err != nil {
		return nil, err
	}

	span := diag_utils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	d.addForwardedHeadersToMetadata(req)
	d.addDestinationAppIDHeaderToMetadata(appID, req)

	clientV1 := internalv1pb.NewServiceInvocationClient(conn)
	resp, err := clientV1.CallLocal(ctx, req.Proto())
	if err != nil {
		return nil, err
	}

	return invokev1.InternalInvokeResponse(resp)
}

func (d *directMessaging) addDestinationAppIDHeaderToMetadata(appID string, req *invokev1.InvokeMethodRequest) {
	req.Metadata()[invokev1.DestinationIDHeader] = &internalv1pb.ListStringValue{
		Values: []string{appID},
	}
}

func (d *directMessaging) addForwardedHeadersToMetadata(req *invokev1.InvokeMethodRequest) {
	metadata := req.Metadata()

	var forwardedHeaderValue string

	if d.hostAddress != "" {
		// Add X-Forwarded-For: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For
		metadata[fasthttp.HeaderXForwardedFor] = &internalv1pb.ListStringValue{
			Values: []string{d.hostAddress},
		}

		forwardedHeaderValue += "for=" + d.hostAddress + ";by=" + d.hostAddress + ";"
	}

	if d.hostName != "" {
		// Add X-Forwarded-Host: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Host
		metadata[fasthttp.HeaderXForwardedHost] = &internalv1pb.ListStringValue{
			Values: []string{d.hostName},
		}

		forwardedHeaderValue += "host=" + d.hostName
	}

	// Add Forwarded header: https://tools.ietf.org/html/rfc7239
	metadata[fasthttp.HeaderForwarded] = &internalv1pb.ListStringValue{
		Values: []string{forwardedHeaderValue},
	}
}

func (d *directMessaging) getRemoteApp(appID string) (remoteApp, error) {
	id, namespace, err := d.requestAppIDAndNamespace(appID)
	if err != nil {
		return remoteApp{}, err
	}

	request := nr.ResolveRequest{ID: id, Namespace: namespace, Port: d.grpcPort}
	address, err := d.resolver.ResolveID(request)
	if err != nil {
		return remoteApp{}, err
	}

	return remoteApp{
		namespace: namespace,
		id:        id,
		address:   address,
	}, nil
}
