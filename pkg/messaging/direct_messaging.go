// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package messaging

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

var log = logger.NewLogger("dapr.runtime.direct_messaging")

// MessageClientConnection is the function type to connect to the other
// applications to send the message using service invocation.
type MessageClientConnection func(address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool, connectionPoolPrefix string) (*grpc.ClientConn, error)

// DirectMessaging is the API interface for invoking a remote app
type DirectMessaging interface {
	Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}

type directMessaging struct {
	appChannel          channel.AppChannel
	connectionCreatorFn MessageClientConnection
	appID               string
	mode                modes.DaprMode
	grpcPort            int
	namespace           string
	resolver            nr.Resolver
	tracingSpec         config.TracingSpec
	hostAddress         string
	hostName            string
	maxRequestBodySize  int
	gatewayEndpoints    map[string]config.GatewayEndpoints
	gatewayEnabled      bool
}

type remoteApp struct {
	id        string
	namespace string
	address   string
	gateway   string
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(
	appID, namespace string,
	port int, mode modes.DaprMode,
	appChannel channel.AppChannel,
	clientConnFn MessageClientConnection,
	resolver nr.Resolver,
	tracingSpec config.TracingSpec,
	maxRequestBodySize int,
	gatewayEndpoints map[string]config.GatewayEndpoints) DirectMessaging {
	hAddr, _ := utils.GetHostAddress()
	hName, _ := os.Hostname()
	gatewayEnabled := len(gatewayEndpoints) > 0 // Perform this check once.
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
		maxRequestBodySize:  maxRequestBodySize,
		gatewayEndpoints:    gatewayEndpoints,
		gatewayEnabled:      gatewayEnabled,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	app, err := d.getRemoteApp(targetAppID)
	if err != nil {
		return nil, err
	}

	if app.id == d.appID && app.namespace == d.namespace && len(app.gateway) == 0 {
		return d.invokeLocal(ctx, req)
	}
	return d.invokeWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, app, d.invokeRemote, req, app.gateway)
}

// parseAppID takes an app id and returns its constituent parts; id, namespace and gateway or error.
func (d *directMessaging) parseAppID(targetAppID string) (string, string, string, error) {
	items := strings.Split(targetAppID, ".")
	switch len(items) {
	case 1: // <appID> - use our namespace.
		{
			return targetAppID, d.namespace, "", nil
		}
	case 2: // <appID>.<namespace> - use provided namespace.
		{
			return items[0], items[1], "", nil
		}
	case 3: // <appID>.<namespace>.<gateway> - use provided namespace and gateway.
		{
			return items[0], items[1], items[2], nil
		}
	default:
		{
			return "", "", "", errors.Errorf("invalid app id %s", targetAppID)
		}
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
	fn func(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest, connPoolPrefix string) (*invokev1.InvokeMethodResponse, error),
	req *invokev1.InvokeMethodRequest,
	connectionPoolPrefix string) (*invokev1.InvokeMethodResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, app.id, app.namespace, app.address, req, connectionPoolPrefix)
		if err == nil {
			return resp, nil
		}
		log.Debugf("retry count: %d, grpc call failed, ns: %s, addr: %s, appid: %s, err: %s",
			i+1, app.namespace, app.address, app.id, err.Error())
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			_, connerr := d.connectionCreatorFn(app.address, app.id, app.namespace, false, true, false, connectionPoolPrefix)
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

func (d *directMessaging) invokeRemote(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest, connectionPoolPrefix string) (*invokev1.InvokeMethodResponse, error) {
	conn, err := d.connectionCreatorFn(appAddress, appID, namespace, false, false, false, connectionPoolPrefix)
	if err != nil {
		return nil, err
	}

	span := diag_utils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	d.addForwardedHeadersToMetadata(req)
	d.addDestinationAppIDHeaderToMetadata(appID, req)

	clientV1 := internalv1pb.NewServiceInvocationClient(conn)

	var opts []grpc.CallOption
	opts = append(opts, grpc.MaxCallRecvMsgSize(d.maxRequestBodySize*1024*1024), grpc.MaxCallSendMsgSize(d.maxRequestBodySize*1024*1024))

	resp, err := clientV1.CallLocal(ctx, req.Proto(), opts...)
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

	addOrCreate := func(header string, value string) {
		if metadata[header] == nil {
			metadata[header] = &internalv1pb.ListStringValue{
				Values: []string{value},
			}
		} else {
			metadata[header].Values = append(metadata[header].Values, value)
		}
	}

	if d.hostAddress != "" {
		// Add X-Forwarded-For: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For
		addOrCreate(fasthttp.HeaderXForwardedFor, d.hostAddress)

		forwardedHeaderValue += "for=" + d.hostAddress + ";by=" + d.hostAddress + ";"
	}

	if d.hostName != "" {
		// Add X-Forwarded-Host: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Host
		addOrCreate(fasthttp.HeaderXForwardedHost, d.hostName)

		forwardedHeaderValue += "host=" + d.hostName
	}

	// Add Forwarded header: https://tools.ietf.org/html/rfc7239
	addOrCreate(fasthttp.HeaderForwarded, forwardedHeaderValue)
}

func (d *directMessaging) getRemoteApp(appID string) (remoteApp, error) {
	id, namespace, gatewayName, err := d.parseAppID(appID)
	if err != nil {
		return remoteApp{}, err
	}

	if len(gatewayName) > 0 {
		if !d.gatewayEnabled {
			return remoteApp{}, errors.Errorf("gateway feature is disabled or no gateways available, invalid app id %s", appID)
		}

		gateway, ok := d.gatewayEndpoints[gatewayName]
		if !ok {
			return remoteApp{}, errors.Errorf("gateway %s not found", gatewayName)
		}

		return remoteApp{
			namespace: namespace,
			id:        id,
			address:   gateway.Address,
			gateway:   gateway.Name,
		}, nil
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
