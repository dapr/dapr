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
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/channel"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
type messageClientConnection func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

// DirectMessaging is the API interface for invoking a remote app.
type DirectMessaging interface {
	Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}

type directMessaging struct {
	appChannel           channel.AppChannel
	connectionCreatorFn  messageClientConnection
	appID                string
	mode                 modes.DaprMode
	grpcPort             int
	namespace            string
	resolver             nr.Resolver
	hostAddress          string
	hostName             string
	maxRequestBodySizeMB int
	proxy                Proxy
	readBufferSize       int
	resiliency           resiliency.Provider
	isResiliencyEnabled  bool
}

type remoteApp struct {
	id        string
	namespace string
	address   string
}

// NewDirectMessaging contains the options for NewDirectMessaging.
type NewDirectMessagingOpts struct {
	AppID               string
	Namespace           string
	Port                int
	Mode                modes.DaprMode
	AppChannel          channel.AppChannel
	ClientConnFn        messageClientConnection
	Resolver            nr.Resolver
	MaxRequestBodySize  int
	Proxy               Proxy
	ReadBufferSize      int
	Resiliency          resiliency.Provider
	IsResiliencyEnabled bool
}

// NewDirectMessaging returns a new direct messaging api.
func NewDirectMessaging(opts NewDirectMessagingOpts) DirectMessaging {
	hAddr, _ := utils.GetHostAddress()
	hName, _ := os.Hostname()

	dm := &directMessaging{
		appID:                opts.AppID,
		namespace:            opts.Namespace,
		grpcPort:             opts.Port,
		mode:                 opts.Mode,
		appChannel:           opts.AppChannel,
		connectionCreatorFn:  opts.ClientConnFn,
		resolver:             opts.Resolver,
		maxRequestBodySizeMB: opts.MaxRequestBodySize,
		proxy:                opts.Proxy,
		readBufferSize:       opts.ReadBufferSize,
		resiliency:           opts.Resiliency,
		isResiliencyEnabled:  opts.IsResiliencyEnabled,
		hostAddress:          hAddr,
		hostName:             hName,
	}

	if dm.proxy != nil {
		dm.proxy.SetRemoteAppFn(dm.getRemoteApp)
		dm.proxy.SetTelemetryFn(dm.setContextSpan)
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
	if targetAppID == "" {
		return "", "", errors.New("app id is empty")
	}
	items := strings.Split(targetAppID, ".")
	switch len(items) {
	case 1:
		return targetAppID, d.namespace, nil
	case 2:
		return items[0], items[1], nil
	default:
		return "", "", fmt.Errorf("invalid app id %s", targetAppID)
	}
}

// invokeWithRetry will call a remote endpoint for the specified number of retries and will only retry in the case of transient failures.
// TODO: check why https://github.com/grpc-ecosystem/go-grpc-middleware/blob/master/retry/examples_test.go doesn't recover the connection when target server shuts down.
func (d *directMessaging) invokeWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	app remoteApp,
	fn func(ctx context.Context, appID, namespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, func(destroy bool), error),
	req *invokev1.InvokeMethodRequest,
) (*invokev1.InvokeMethodResponse, error) {
	// TODO: Once resiliency is out of preview, we can have this be the only path.
	if d.isResiliencyEnabled {
		if d.resiliency.GetPolicy(app.id, &resiliency.EndpointPolicy{}) == nil {
			policyRunner := resiliency.NewRunner[*invokev1.InvokeMethodResponse](ctx,
				d.resiliency.BuiltInPolicy(resiliency.BuiltInServiceRetries),
			)
			attempts := atomic.Int32{}
			return policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
				attempt := attempts.Add(1)
				rResp, teardown, rErr := fn(ctx, app.id, app.namespace, app.address, req)
				if rErr == nil {
					teardown(false)
					return rResp, nil
				}

				code := status.Code(rErr)
				if code == codes.Unavailable || code == codes.Unauthenticated {
					// Destroy the connection and force a re-connection on the next attempt
					teardown(true)
					return rResp, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", app.id, attempt-1, rErr)
				}
				teardown(false)
				return rResp, backoff.Permanent(rErr)
			})
		}

		resp, teardown, err := fn(ctx, app.id, app.namespace, app.address, req)
		teardown(false)
		return resp, err
	}
	for i := 0; i < numRetries; i++ {
		resp, teardown, err := fn(ctx, app.id, app.namespace, app.address, req)
		if err == nil {
			teardown(false)
			return resp, nil
		}
		log.Debugf("retry count: %d, grpc call failed, ns: %s, addr: %s, appid: %s, err: %s",
			i+1, app.namespace, app.address, app.id, err.Error())
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			// Destroy the connection and force a re-connection on the next attempt
			teardown(true)
			continue
		}
		teardown(false)
		return resp, err
	}
	return nil, fmt.Errorf("failed to invoke target %s after %v retries", app.id, numRetries)
}

func (d *directMessaging) invokeLocal(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	return d.appChannel.InvokeMethod(ctx, req)
}

func (d *directMessaging) setContextSpan(ctx context.Context) context.Context {
	span := diagUtils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	return ctx
}

func (d *directMessaging) invokeRemote(ctx context.Context, appID, appNamespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, func(destroy bool), error) {
	conn, teardown, err := d.connectionCreatorFn(context.TODO(), appAddress, appID, appNamespace)
	if err != nil {
		return nil, nil, err
	}

	ctx = d.setContextSpan(ctx)

	d.addForwardedHeadersToMetadata(req)
	d.addDestinationAppIDHeaderToMetadata(appID, req)
	d.addCallerAndCalleeAppIDHeaderToMetadata(d.appID, appID, req)

	clientV1 := internalv1pb.NewServiceInvocationClient(conn)

	opts := []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(d.maxRequestBodySizeMB << 20),
		grpc.MaxCallSendMsgSize(d.maxRequestBodySizeMB << 20),
	}

	start := time.Now()
	diag.DefaultMonitoring.ServiceInvocationRequestSent(appID, req.Message().Method)

	var resp *internalv1pb.InternalInvokeResponse
	defer func() {
		if resp != nil {
			diag.DefaultMonitoring.ServiceInvocationResponseReceived(appID, req.Message().Method, resp.Status.Code, start)
		}
	}()

	resp, err = clientV1.CallLocal(ctx, req.Proto(), opts...)
	if err != nil {
		return nil, teardown, err
	}

	imr, err := invokev1.InternalInvokeResponse(resp)
	if err != nil {
		return nil, nil, err
	}
	return imr, teardown, err
}

func (d *directMessaging) addDestinationAppIDHeaderToMetadata(appID string, req *invokev1.InvokeMethodRequest) {
	req.Metadata()[invokev1.DestinationIDHeader] = &internalv1pb.ListStringValue{
		Values: []string{appID},
	}
}

func (d *directMessaging) addCallerAndCalleeAppIDHeaderToMetadata(callerAppID, calleeAppID string, req *invokev1.InvokeMethodRequest) {
	req.Metadata()[invokev1.CallerIDHeader] = &internalv1pb.ListStringValue{
		Values: []string{callerAppID},
	}
	req.Metadata()[invokev1.CalleeIDHeader] = &internalv1pb.ListStringValue{
		Values: []string{calleeAppID},
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

	if d.resolver == nil {
		return remoteApp{}, errors.New("name resolver not initialized")
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
