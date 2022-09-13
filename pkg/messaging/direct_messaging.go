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
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

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
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
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
		appID:               opts.AppID,
		namespace:           opts.Namespace,
		grpcPort:            opts.Port,
		mode:                opts.Mode,
		appChannel:          opts.AppChannel,
		connectionCreatorFn: opts.ClientConnFn,
		resolver:            opts.Resolver,
		maxRequestBodySize:  opts.MaxRequestBodySize,
		proxy:               opts.Proxy,
		readBufferSize:      opts.ReadBufferSize,
		resiliency:          opts.Resiliency,
		isResiliencyEnabled: opts.IsResiliencyEnabled,
		hostAddress:         hAddr,
		hostName:            hName,
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
		if d.resiliency.GetPolicy(app.id, &resiliency.EndpointPolicy{}) == nil {
			var (
				resp                 *invokev1.InvokeMethodResponse
				retriesExhaustedPath bool // Used to track final error state.
				nullifyResponsePath  bool // Used to track final response state.
			)
			policy := d.resiliency.BuiltInPolicy(ctx, resiliency.BuiltInServiceRetries)
			// This policy has built-in retries so enable replay in the request
			req.WithReplay(true)
			err := policy(func(ctx context.Context) (rErr error) {
				if resp != nil {
					resp.Close()
				}
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
				if resp != nil {
					resp.Close()
				}
				resp = nil
			}

			return resp, err
		}
		return fn(ctx, app.id, app.namespace, app.address, req)
	}

	// We need to enable replaying because the request may be attempted again in this path
	req.WithReplay(true)
	var (
		resp *invokev1.InvokeMethodResponse
		err  error
	)
	for i := 0; i < numRetries; i++ {
		if resp != nil {
			resp.Close()
		}
		resp, err = fn(ctx, app.id, app.namespace, app.address, req)
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
	span := diagUtils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())

	return ctx
}

func (d *directMessaging) invokeRemoteUnary(ctx context.Context, clientV1 internalv1pb.ServiceInvocationClient, reqProto *internalv1pb.InternalInvokeRequest, opts []grpc.CallOption) (*invokev1.InvokeMethodResponse, error) {
	var resp *internalv1pb.InternalInvokeResponse
	resp, err := clientV1.CallLocal(ctx, reqProto, opts...)
	if err != nil {
		return nil, err
	}
	return invokev1.InternalInvokeResponse(resp)
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

	opts := []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(d.maxRequestBodySize * 1024 * 1024),
		grpc.MaxCallSendMsgSize(d.maxRequestBodySize * 1024 * 1024),
	}

	// If the request contains a message in the body, send an unary request
	reqProto := req.Proto()
	if req.HasMessageData() {
		return d.invokeRemoteUnary(ctx, clientV1, reqProto, opts)
	}

	// Send the request using a stream
	stream, err := clientV1.CallLocalStream(ctx, opts...)
	if err != nil {
		// If we're connecting to a sidecar that doesn't support CallLocalStream, fallback to the unary RPC
		if status.Code(err) == codes.Unimplemented {
			log.Warnf("App %s does not support streaming-based service invocation (likely due to being on an older version of Dapr); falling back to unary calls", appID)
			reqProto, err = req.ProtoWithData()
			if err != nil {
				return nil, err
			}
			return d.invokeRemoteUnary(ctx, clientV1, reqProto, opts)
		}
		return nil, err
	}
	r := req.RawData()
	buf := make([]byte, 4096) // 4KB buffer
	var (
		proto *internalv1pb.InternalInvokeRequestStream
		n     int
	)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		proto = &internalv1pb.InternalInvokeRequestStream{
			Payload: &commonv1pb.StreamPayload{},
		}

		// First message only
		if reqProto != nil {
			proto.Request = reqProto
			reqProto = nil
		}

		if r != nil {
			n, err = r.Read(buf)
			if err == io.EOF {
				proto.Payload.Complete = true
				err = nil
			} else if err != nil {
				return nil, err
			}
			if n > 0 {
				proto.Payload.Data = &anypb.Any{
					Value: buf[:n],
				}
			}
		} else {
			proto.Payload.Complete = true
		}
		err = stream.Send(proto)
		if err != nil {
			return nil, err
		}

		// Stop with the last chunk
		if proto.Payload.Complete {
			break
		}
	}

	// Read the first chunk of the response
	chunk := &internalv1pb.InternalInvokeResponseStream{}
	err = stream.RecvMsg(chunk)
	if err != nil {
		return nil, err
	}
	if chunk.Response == nil || chunk.Response.Status == nil || chunk.Response.Headers == nil {
		return nil, errors.New("response does not contain the required fields in the leading chunk")
	}
	pr, pw := io.Pipe()
	res, err := invokev1.InternalInvokeResponse(chunk.Response)
	if err != nil {
		return nil, err
	}
	var ct string
	if chunk.Response.Message != nil {
		ct = chunk.Response.Message.ContentType
	}
	res.WithRawData(pr, ct)

	// Read the response into the stream in the background
	go func() {
		var (
			done    bool
			readErr error
		)
		for {
			if ctx.Err() != nil {
				pw.CloseWithError(ctx.Err())
				return
			}

			done, readErr = ReadChunk(chunk, pw)
			if readErr != nil {
				pw.CloseWithError(readErr)
				return
			}

			if done {
				break
			}

			readErr = stream.RecvMsg(chunk)
			if err != nil {
				pw.CloseWithError(readErr)
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

// Interface for *internalv1pb.InternalInvokeResponseStream and *internalv1pb.InternalInvokeRequestStream
type chunkWithPayload interface {
	GetPayload() *commonv1pb.StreamPayload
}

// ReadChunk reads a chunk of data from an InternalInvokeResponseStream or InternalInvokeRequestStream object
func ReadChunk(chunk chunkWithPayload, out io.Writer) (done bool, err error) {
	payload := chunk.GetPayload()
	if payload == nil {
		return false, nil
	}

	if payload.Complete {
		done = true
	}

	if payload.Data != nil && len(payload.Data.Value) > 0 {
		var n int
		n, err = out.Write(payload.Data.Value)
		if err != nil {
			return false, err
		}
		if n != len(payload.Data.Value) {
			return false, fmt.Errorf("wrote %d out of %d bytes", n, len(payload.Data.Value))
		}
	}

	return done, nil
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
