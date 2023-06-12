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
	"io"
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
	"github.com/dapr/dapr/pkg/channel"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.direct_messaging")

const streamingUnsupportedErr = "streaming-based service invocation is enabled, but target app %s is running a version of Dapr that does not support it"

// messageClientConnection is the function type to connect to the other
// applications to send the message using service invocation.
type messageClientConnection func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

// DirectMessaging is the API interface for invoking a remote app.
type DirectMessaging interface {
	Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	SetAppChannel(appChannel channel.AppChannel)
	SetHTTPEndpointsAppChannel(appChannel channel.HTTPEndpointAppChannel)
}

type directMessaging struct {
	appChannel              channel.AppChannel
	httpEndpointsAppChannel channel.HTTPEndpointAppChannel
	connectionCreatorFn     messageClientConnection
	appID                   string
	mode                    modes.DaprMode
	grpcPort                int
	namespace               string
	resolver                nr.Resolver
	hostAddress             string
	hostName                string
	maxRequestBodySizeMB    int
	proxy                   Proxy
	readBufferSize          int
	resiliency              resiliency.Provider
	isStreamingEnabled      bool
	compStore               *compstore.ComponentStore
}

type remoteApp struct {
	id        string
	namespace string
	address   string
}

// NewDirectMessaging contains the options for NewDirectMessaging.
type NewDirectMessagingOpts struct {
	AppID                   string
	Namespace               string
	Port                    int
	CompStore               *compstore.ComponentStore
	Mode                    modes.DaprMode
	AppChannel              channel.AppChannel
	HTTPEndpointsAppChannel channel.HTTPEndpointAppChannel
	ClientConnFn            messageClientConnection
	Resolver                nr.Resolver
	MaxRequestBodySize      int
	Proxy                   Proxy
	ReadBufferSize          int
	Resiliency              resiliency.Provider
	IsStreamingEnabled      bool
}

// NewDirectMessaging returns a new direct messaging api.
func NewDirectMessaging(opts NewDirectMessagingOpts) DirectMessaging {
	hAddr, _ := utils.GetHostAddress()
	hName, _ := os.Hostname()

	dm := &directMessaging{
		appID:                   opts.AppID,
		namespace:               opts.Namespace,
		grpcPort:                opts.Port,
		mode:                    opts.Mode,
		appChannel:              opts.AppChannel,
		httpEndpointsAppChannel: opts.HTTPEndpointsAppChannel,
		connectionCreatorFn:     opts.ClientConnFn,
		resolver:                opts.Resolver,
		maxRequestBodySizeMB:    opts.MaxRequestBodySize,
		proxy:                   opts.Proxy,
		readBufferSize:          opts.ReadBufferSize,
		resiliency:              opts.Resiliency,
		isStreamingEnabled:      opts.IsStreamingEnabled,
		hostAddress:             hAddr,
		hostName:                hName,
		compStore:               opts.CompStore,
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

	// invoke external calls first if appID matches an httpEndpoint.Name or app.id == baseURL that is overwritten
	if d.isHTTPEndpoint(app.id) || strings.HasPrefix(app.id, "http://") || strings.HasPrefix(app.id, "https://") {
		return d.invokeWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, app, d.invokeHTTPEndpoint, req)
	}

	if app.id == d.appID && app.namespace == d.namespace {
		return d.invokeLocal(ctx, req)
	}

	return d.invokeWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, app, d.invokeRemote, req)
}

// SetAppChannel sets the appChannel property in the object.
func (d *directMessaging) SetAppChannel(appChannel channel.AppChannel) {
	d.appChannel = appChannel
}

// SetHTTPEndpointsAppChannel sets the appChannel property in the object.
func (d *directMessaging) SetHTTPEndpointsAppChannel(appChannel channel.HTTPEndpointAppChannel) {
	d.httpEndpointsAppChannel = appChannel
}

// requestAppIDAndNamespace takes an app id and returns the app id, namespace and error.
func (d *directMessaging) requestAppIDAndNamespace(targetAppID string) (string, string, error) {
	if targetAppID == "" {
		return "", "", errors.New("app id is empty")
	}
	// external invocation with targetAppID == baseURL
	if strings.HasPrefix(targetAppID, "http://") || strings.HasPrefix(targetAppID, "https://") {
		return targetAppID, "", nil
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

// checkHTTPEndpoints takes an app id and checks if the app id is associated with the http endpoint CRDs,
// and returns the baseURL if an http endpoint is found.
func (d *directMessaging) checkHTTPEndpoints(targetAppID string) string {
	endpoint, ok := d.compStore.GetHTTPEndpoint(targetAppID)
	if ok {
		if endpoint.Name == targetAppID {
			return endpoint.Spec.BaseURL
		}
	}

	return ""
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
	if !d.resiliency.PolicyDefined(app.id, resiliency.EndpointPolicy{}) {
		// This policy has built-in retries so enable replay in the request
		req.WithReplay(true)

		policyRunner := resiliency.NewRunnerWithOptions(ctx,
			d.resiliency.BuiltInPolicy(resiliency.BuiltInServiceRetries),
			resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
				Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
			},
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

func (d *directMessaging) invokeLocal(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	return d.appChannel.InvokeMethod(ctx, req, "")
}

func (d *directMessaging) setContextSpan(ctx context.Context) context.Context {
	span := diagUtils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	return ctx
}

func (d *directMessaging) isHTTPEndpoint(appID string) bool {
	_, ok := d.compStore.GetHTTPEndpoint(appID)
	return ok
}

func (d *directMessaging) invokeHTTPEndpoint(ctx context.Context, appID, appNamespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, func(destroy bool), error) {
	ctx = d.setContextSpan(ctx)

	// Set up timers
	start := time.Now()
	diag.DefaultMonitoring.ServiceInvocationRequestSent(appID, req.Message().Method)
	imr, err := d.invokeRemoteUnaryForHTTPEndpoint(ctx, nil, req, nil, appID)

	// Diagnostics
	if imr != nil {
		diag.DefaultMonitoring.ServiceInvocationResponseReceived(appID, req.Message().Method, imr.Status().Code, start)
	}

	return imr, nopTeardown, err
}

func (d *directMessaging) invokeRemote(ctx context.Context, appID, appNamespace, appAddress string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, func(destroy bool), error) {
	conn, teardown, err := d.connectionCreatorFn(context.TODO(), appAddress, appID, appNamespace)
	if err != nil {
		if teardown == nil {
			teardown = nopTeardown
		}
		return nil, teardown, err
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

	// Set up timers
	start := time.Now()
	diag.DefaultMonitoring.ServiceInvocationRequestSent(appID, req.Message().Method)

	var imr *invokev1.InvokeMethodResponse
	if !d.isStreamingEnabled {
		imr, err = d.invokeRemoteUnary(ctx, clientV1, req, opts)
	} else {
		imr, err = d.invokeRemoteStream(ctx, clientV1, req, appID, opts)
	}

	// Diagnostics
	if imr != nil {
		diag.DefaultMonitoring.ServiceInvocationResponseReceived(appID, req.Message().Method, imr.Status().Code, start)
	}

	return imr, teardown, err
}

func (d *directMessaging) invokeRemoteUnaryForHTTPEndpoint(ctx context.Context, clientV1 internalv1pb.ServiceInvocationClient, req *invokev1.InvokeMethodRequest, opts []grpc.CallOption, appID string) (*invokev1.InvokeMethodResponse, error) {
	if d.httpEndpointsAppChannel == nil {
		return nil, errors.New("cannot invoke http endpoint: http endpoints app channel not initialized")
	}

	return d.httpEndpointsAppChannel.InvokeMethod(ctx, req, appID)
}

func (d *directMessaging) invokeRemoteUnary(ctx context.Context, clientV1 internalv1pb.ServiceInvocationClient, req *invokev1.InvokeMethodRequest, opts []grpc.CallOption) (*invokev1.InvokeMethodResponse, error) {
	pd, err := req.ProtoWithData()
	if err != nil {
		return nil, fmt.Errorf("failed to read data from request object: %w", err)
	}

	resp, err := clientV1.CallLocal(ctx, pd, opts...)
	if err != nil {
		return nil, err
	}

	return invokev1.InternalInvokeResponse(resp)
}

func (d *directMessaging) invokeRemoteStream(ctx context.Context, clientV1 internalv1pb.ServiceInvocationClient, req *invokev1.InvokeMethodRequest, appID string, opts []grpc.CallOption) (*invokev1.InvokeMethodResponse, error) {
	stream, err := clientV1.CallLocalStream(ctx, opts...)
	if err != nil {
		return nil, err
	}
	buf := invokev1.BufPool.Get().(*[]byte)
	defer func() {
		invokev1.BufPool.Put(buf)
	}()
	r := req.RawData()
	reqProto := req.Proto()
	proto := &internalv1pb.InternalInvokeRequestStream{}
	var (
		n    int
		seq  uint64
		done bool
	)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// First message only - add the request
		if reqProto != nil {
			proto.Request = reqProto
			reqProto = nil
		} else {
			// Reset the object so we can re-use it
			proto.Reset()
		}

		if r != nil {
			n, err = r.Read(*buf)
			if err == io.EOF {
				done = true
			} else if err != nil {
				return nil, err
			}
			if n > 0 {
				proto.Payload = &commonv1pb.StreamPayload{
					Data: (*buf)[:n],
					Seq:  seq,
				}
				seq++
			}
		} else {
			done = true
		}

		// Send the chunk if there's anything to send
		if proto.Request != nil || proto.Payload != nil {
			err = stream.SendMsg(proto)
			if errors.Is(err, io.EOF) {
				// If SendMsg returns an io.EOF error, it usually means that there's a transport-level error
				// The exact error can only be determined by RecvMsg, so if we encounter an EOF error here, just consider the stream done and let RecvMsg handle the error
				done = true
			} else if err != nil {
				return nil, fmt.Errorf("error sending message: %w", err)
			}
		}

		// Stop with the last chunk
		if done {
			err = stream.CloseSend()
			if err != nil {
				return nil, fmt.Errorf("failed to close the send direction of the stream: %w", err)
			}
			break
		}
	}

	// Read the first chunk of the response
	chunk := &internalv1pb.InternalInvokeResponseStream{}
	err = stream.RecvMsg(chunk)
	if err != nil {
		// If we get an "Unimplemented" status code, it means that we're connecting to a sidecar that doesn't support CallLocalStream
		// This happens if we're connecting to an older version of daprd
		// What we do here depends on whether the request is replayable:
		// - If the request is replayable, we will re-submit it as unary. This will have a small performance impact due to the additional round-trip, but it will still work (and the warning will remind users to upgrade)
		// - If the request is not replayable, the data stream has already been consumed at this point so nothing else we can do - just show an error and tell users to upgrade the target app… (or disable streaming for now)
		// At this point it seems that this is the best we can do, since we cannot detect Unimplemented status codes earlier (unless we send a "ping", which would add latency).
		// See: // See: https://github.com/grpc/grpc-go/issues/5910
		if status.Code(err) == codes.Unimplemented {
			if req.CanReplay() {
				log.Warnf("App %s does not support streaming-based service invocation (most likely because it's using an older version of Dapr); falling back to unary calls", appID)
				return d.invokeRemoteUnary(ctx, clientV1, req, opts)
			} else {
				log.Errorf("App %s does not support streaming-based service invocation (most likely because it's using an older version of Dapr) and the request is not replayable. Please upgrade the Dapr sidecar used by the target app, or use Resiliency policies to add retries", appID)
				return nil, fmt.Errorf(streamingUnsupportedErr, appID)
			}
		}
		return nil, err
	}
	if chunk.Response == nil || chunk.Response.Status == nil {
		return nil, errors.New("response does not contain the required fields in the leading chunk")
	}
	pr, pw := io.Pipe()
	res, err := invokev1.InternalInvokeResponse(chunk.Response)
	if err != nil {
		return nil, err
	}
	res.WithRawData(pr)
	if chunk.Response.Message != nil {
		res.WithContentType(chunk.Response.Message.ContentType)
	}

	// Read the response into the stream in the background
	go func() {
		var (
			expectSeq uint64
			readSeq   uint64
			payload   *commonv1pb.StreamPayload
			readErr   error
		)
		for {
			if ctx.Err() != nil {
				pw.CloseWithError(ctx.Err())
				return
			}

			// Get the payload from the chunk that was previously read
			payload = chunk.GetPayload()
			if payload != nil {
				readSeq, readErr = ReadChunk(payload, pw)
				if readErr != nil {
					pw.CloseWithError(readErr)
					return
				}

				// Check if the sequence number is greater than the previous
				if readSeq != expectSeq {
					pw.CloseWithError(fmt.Errorf("invalid sequence number received: %d (expected: %d)", readSeq, expectSeq))
					return
				}
				expectSeq++
			}

			// Read the next chunk
			readErr = stream.RecvMsg(chunk)
			if errors.Is(readErr, io.EOF) {
				// Receiving an io.EOF signifies that the client has stopped sending data over the pipe, so we can stop reading
				break
			} else if readErr != nil {
				pw.CloseWithError(fmt.Errorf("error receiving message: %w", readErr))
				return
			}

			if chunk.Response != nil && (chunk.Response.Status != nil || chunk.Response.Headers != nil || chunk.Response.Message != nil) {
				pw.CloseWithError(errors.New("response metadata found in non-leading chunk"))
				return
			}
		}

		pw.Close()
	}()

	return res, nil
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

	var address string
	// Note: check for case where URL is overwritten for external service invocation,
	// or if current app id is associated with an http endpoint CRD.
	// This will also forgo service discovery.
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
		address = id
	} else if d.isHTTPEndpoint(id) {
		address = d.checkHTTPEndpoints(id)
	} else {
		request := nr.ResolveRequest{ID: id, Namespace: namespace, Port: d.grpcPort}
		address, err = d.resolver.ResolveID(request)
		if err != nil {
			return remoteApp{}, err
		}
	}

	return remoteApp{
		namespace: namespace,
		id:        id,
		address:   address,
	}, nil
}

// ReadChunk reads a chunk of data from a StreamPayload object.
// The returned value "seq" indicates the sequence number
func ReadChunk(payload *commonv1pb.StreamPayload, out io.Writer) (seq uint64, err error) {
	if len(payload.Data) > 0 {
		var n int
		n, err = out.Write(payload.Data)
		if err != nil {
			return 0, err
		}
		if n != len(payload.Data) {
			return 0, fmt.Errorf("wrote %d out of %d bytes", n, len(payload.Data))
		}
	}

	return payload.Seq, nil
}
