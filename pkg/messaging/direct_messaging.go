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
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/utils"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

var log = logger.NewLogger("dapr.runtime.direct_messaging")

// messageClientConnection is the function type to connect to the other
// applications to send the message using service invocation.
type messageClientConnection func(ctx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(), error)

// DirectMessaging is the API interface for invoking a remote app.
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
	maxRequestBodySize  int
	proxy               Proxy
	readBufferSize      int
	resiliency          resiliency.Provider
	isResiliencyEnabled bool
}

type remoteApp struct {
	id        string
	namespace string
	address   string
}

// NewDirectMessaging returns a new direct messaging api.
func NewDirectMessaging(
	appID, namespace string,
	port int, mode modes.DaprMode,
	appChannel channel.AppChannel,
	clientConnFn messageClientConnection,
	resolver nr.Resolver,
	tracingSpec config.TracingSpec,
	maxRequestBodySize int,
	proxy Proxy,
	readBufferSize int,
	streamRequestBody bool,
	resiliency resiliency.Provider,
	isResiliencyEnabled bool,
) DirectMessaging {
	hAddr, _ := utils.GetHostAddress()
	hName, _ := os.Hostname()

	dm := &directMessaging{
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
		proxy:               proxy,
		readBufferSize:      readBufferSize,
		resiliency:          resiliency,
		isResiliencyEnabled: isResiliencyEnabled,
	}

	if proxy != nil {
		proxy.SetRemoteAppFn(dm.getRemoteApp)
		proxy.SetTelemetryFn(dm.setContextSpan)
	}

	return dm
}

// Invoke takes a message requests and invokes an app, either local or remote.
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
	req *invokev1.InvokeMethodRequest,
) (*invokev1.InvokeMethodResponse, error) {
	// TODO: Once resiliency is out of preview, we can have this be the only path.
	if d.isResiliencyEnabled {
		if !d.resiliency.PolicyDefined(app.id, resiliency.Endpoint) {
			retriesExhaustedPath := false // Used to track final error state.
			nullifyResponsePath := false  // Used to track final response state.
			policy := d.resiliency.BuiltInPolicy(ctx, resiliency.BuiltInServiceRetries)
			var resp *invokev1.InvokeMethodResponse
			err := policy(func(ctx context.Context) (rErr error) {
				retriesExhaustedPath = false
				resp, rErr = fn(ctx, app.id, app.namespace, app.address, req)
				if rErr == nil {
					return nil
				}

				code := status.Code(rErr)
				if code == codes.Unavailable || code == codes.Unauthenticated {
					_, teardown, connerr := d.connectionCreatorFn(ctx, app.address, app.id, app.namespace, false, true, false)
					defer teardown()
					if connerr != nil {
						nullifyResponsePath = true
						return backoff.Permanent(connerr)
					}
					retriesExhaustedPath = true
					return rErr
				}
				return backoff.Permanent(rErr)
			})
			// To maintain consistency with the existing built-in retries, we do some transformations/error handling.
			if retriesExhaustedPath {
				return nil, errors.Errorf("failed to invoke target %s after %v retries. Error: %s", app.id, numRetries, err.Error())
			}

			if nullifyResponsePath {
				resp = nil
			}

			return resp, err
		}
		return fn(ctx, app.id, app.namespace, app.address, req)
	}
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, app.id, app.namespace, app.address, req)
		if err == nil {
			return resp, nil
		}
		log.Debugf("retry count: %d, grpc call failed, ns: %s, addr: %s, appid: %s, err: %s",
			i+1, app.namespace, app.address, app.id, err.Error())
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			_, teardown, connerr := d.connectionCreatorFn(context.TODO(), app.address, app.id, app.namespace, false, true, false)
			defer teardown()
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

func (d *directMessaging) setContextSpan(ctx context.Context) context.Context {
	span := diag_utils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	return ctx
}

func (d *directMessaging) invokeRemote(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	conn, teardown, err := d.connectionCreatorFn(context.TODO(), appAddress, appID, namespace, false, false, false)
	defer teardown()
	if err != nil {
		return nil, err
	}

	ctx = d.setContextSpan(ctx)

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
