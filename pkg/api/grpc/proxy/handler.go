// Based on https://github.com/trusch/grpc-proxy
// Copyright Michal Witkowski. Licensed under Apache2 license: https://github.com/trusch/grpc-proxy/blob/master/LICENSE.txt

package proxy

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/api/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/utils"
)

// Metadata header used to indicate if the call should be handled as a gRPC stream.
const StreamMetadataKey = "dapr-stream"

var clientStreamDescForProxying = &grpc.StreamDesc{
	ServerStreams: true,
	ClientStreams: true,
}

// Error returned when the user is trying to make streaming RPCs and the Resiliency policy has retries enabled.
var errRetryOnStreamingRPC = status.Error(codes.FailedPrecondition, "cannot use resiliency policies with retries on streaming RPCs")

type replayBufferCh chan *codec.Frame

type getPolicyFn func(appID, methodName string) *resiliency.PolicyDefinition

// RegisterService sets up a proxy handler for a particular gRPC service and method.
// The behaviour is the same as if you were registering a handler method, e.g. from a codegenerated pb.go file.
//
// This can *only* be used if the `server` also uses grpcproxy.CodecForServer() ServerOption.
func RegisterService(server *grpc.Server, director StreamDirector, getPolicyFn getPolicyFn, serviceName string, methodNames ...string) {
	streamer := &handler{
		director:    director,
		getPolicyFn: getPolicyFn,
	}
	fakeDesc := &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*any)(nil),
	}
	for _, m := range methodNames {
		streamDesc := grpc.StreamDesc{
			StreamName:    m,
			Handler:       streamer.handler,
			ServerStreams: true,
			ClientStreams: true,
		}
		fakeDesc.Streams = append(fakeDesc.Streams, streamDesc)
	}
	server.RegisterService(fakeDesc, streamer)
}

// TransparentHandler returns a handler that attempts to proxy all requests that are not registered in the server.
// The indented use here is as a transparent proxy, where the server doesn't know about the services implemented by the
// backends. It should be used as a `grpc.UnknownServiceHandler`.
//
// This can *only* be used if the `server` also uses grpcproxy.CodecForServer() ServerOption.
func TransparentHandler(director StreamDirector, getPolicyFn getPolicyFn, connFactory DirectorConnectionFactory, maxMessageBodySizeMB int) grpc.StreamHandler {
	streamer := &handler{
		director:           director,
		getPolicyFn:        getPolicyFn,
		connFactory:        connFactory,
		maxRequestBodySize: maxMessageBodySizeMB,
	}
	return streamer.handler
}

type handler struct {
	director           StreamDirector
	getPolicyFn        getPolicyFn
	connFactory        DirectorConnectionFactory
	maxRequestBodySize int
}

// handler is where the real magic of proxying happens.
// It is invoked like any gRPC server stream and uses the gRPC server framing to get and receive bytes from the wire,
// forwarding it to a ClientStream established against the relevant ClientConn.
func (s *handler) handler(srv any, serverStream grpc.ServerStream) error {
	fullMethodName, ok := grpc.MethodFromServerStream(serverStream)
	if !ok {
		return status.Errorf(codes.Internal, "full method name not found in stream")
	}

	// Fetch the AppId so we can reference it for resiliency.
	ctx := serverStream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	v := md[diagnostics.GRPCProxyAppIDKey]

	// The app id check is handled in the StreamDirector. If we don't have it here, we just use a NoOp policy since we know the request is impossible.
	var policyDef *resiliency.PolicyDefinition
	var grpcDestinationAppID string
	if len(v) == 0 || s.getPolicyFn == nil {
		policyDef = resiliency.NoOp{}.EndpointPolicy("", "")
	} else {
		policyDef = s.getPolicyFn(v[0], fullMethodName)
		grpcDestinationAppID = v[0]
	}

	// When using resiliency, we need to put special care in handling proxied gRPC requests that are streams, because these can be long-lived.
	// - For unary gRPC calls, we need to apply the timeout and retry policies to the entire call, from start to end
	// - For streaming gRPC calls, timeouts and retries should only kick in during the initial "handshake". After that, the connection is to be considered established and we should continue with it until it's stopped or canceled or failed. Errors after the initial handshake should be sent directly to the client and server and not handled by Dapr.
	// With gRPC, every call is, at its core, a stream. The gRPC library maintains a list of which calls are to be interpreted as "unary" RPCs, and then "wraps them" so users can write code that behaves like a regular RPC without having to worry about underlying streams. This is possible by having knowledge of the proto files.
	// Because Dapr doesn't have the protos that are used for gRPC proxying, we cannot determine if a RPC is stream-based or "unary", so we can't do what the gRPC library does.
	// Instead, we're going to rely on the "dapr-stream" boolean header: if set, we consider the RPC as stream-based and apply Resiliency features (timeouts and retries) only to the initial handshake.
	var isStream bool
	streamCheckValue := md[StreamMetadataKey]
	if len(streamCheckValue) > 0 {
		isStream = utils.IsTruthy(streamCheckValue[0])
	}

	var replayBuffer replayBufferCh
	if !isStream && policyDef != nil && policyDef.HasRetries() {
		replayBuffer = make(replayBufferCh, 1)
	}

	clientStreamOptSubtype := make([]grpc.CallOption, 0, 4)
	clientStreamOptSubtype = append(clientStreamOptSubtype, grpc.CallContentSubtype((&codec.Proxy{}).Name()))

	if s.maxRequestBodySize > 0 {
		clientStreamOptSubtype = append(clientStreamOptSubtype,
			grpc.MaxCallRecvMsgSize(s.maxRequestBodySize<<20),
			grpc.MaxCallSendMsgSize(s.maxRequestBodySize<<20),
			grpc.MaxRetryRPCBufferSize(s.maxRequestBodySize<<20),
		)
	}

	headersSent := &atomic.Bool{}
	counter := atomic.Int32{}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef, resiliency.RunnerOpts[*proxyRunner]{
		Disposer: func(pr *proxyRunner) {
			pr.dispose()
		},
	})
	var requestStartedAt time.Time
	pr, cErr := policyRunner(func(ctx context.Context) (*proxyRunner, error) {
		// Get the current iteration count
		iter := counter.Add(1)

		// If we're using streams, we need to replace ctx with serverStream.Context(), as ctx is canceled when this method returns
		if isStream {
			ctx = serverStream.Context()
		}

		// We require that the director's returned context inherits from the server stream's context (directly or through ctx)
		outgoingCtx, backendConn, target, teardown, err := s.director(ctx, fullMethodName)
		// Do not "defer teardown(false)" yet, in case we need to proxy a stream
		if err != nil {
			teardown(false)
			return nil, err
		}

		// Do not "defer clientCancel()" yet, in case we need to proxy a stream
		clientCtx, clientCancel := context.WithCancel(outgoingCtx)

		requestStartedAt = time.Now()
		if grpcDestinationAppID != "" {
			if isStream {
				diagnostics.DefaultMonitoring.ServiceInvocationStreamingRequestSent(grpcDestinationAppID)
			} else {
				diagnostics.DefaultMonitoring.ServiceInvocationRequestSent(grpcDestinationAppID)
			}
		}

		// (The next TODO comes from the original author of the library we adapted - leaving it here in case we want to do that for our own reasons)
		// TODO(mwitkow): Add a `forwarded` header to metadata, https://en.wikipedia.org/wiki/X-Forwarded-For.
		clientStream, err := grpc.NewClientStream(
			clientCtx,
			clientStreamDescForProxying,
			backendConn,
			fullMethodName,
			clientStreamOptSubtype...,
		)
		if err != nil {
			var reconnectionSucceeded bool
			code := status.Code(err)
			defer func() {
				// if we could not reconnect just create the response metrics for the connection error
				if !reconnectionSucceeded && grpcDestinationAppID != "" {
					if !isStream {
						diagnostics.DefaultMonitoring.ServiceInvocationResponseReceived(grpcDestinationAppID, int32(code), requestStartedAt)
					} else {
						diagnostics.DefaultMonitoring.ServiceInvocationStreamingResponseReceived(grpcDestinationAppID, int32(code))
					}
				}
			}()

			if target != nil && (code == codes.Unavailable || code == codes.Unauthenticated) {
				// It's possible that we get to this point while another goroutine is executing the same policy function.
				// For example, this could happen if this iteration has timed out and "policyRunner" has triggered a new execution already.
				// In this case, we should not teardown the connection because it could being used by the next execution. So just return and move on.
				if counter.Load() != iter {
					clientCancel()
					teardown(false)
					return nil, err
				}

				// Destroy the connection so it can be recreated
				teardown(true)

				// Re-connect
				backendConn, teardown, err = s.connFactory(outgoingCtx, target.Address, target.ID, target.Namespace)
				if err != nil {
					teardown(false)
					clientCancel()
					return nil, err
				}

				clientStream, err = grpc.NewClientStream(clientCtx, clientStreamDescForProxying, backendConn, fullMethodName, clientStreamOptSubtype...)
				if err != nil {
					code = status.Code(err)
					teardown(false)
					clientCancel()
					return nil, err
				}
				reconnectionSucceeded = true
			} else {
				teardown(false)
				clientCancel()
				return nil, err
			}
		}

		// Create the proxyRunner that will execute the proxying
		pr := proxyRunner{
			serverStream: serverStream,
			clientStream: clientStream,
			replayBuffer: replayBuffer,
			headersSent:  headersSent,
			clientCtx:    clientCtx,
			clientCancel: clientCancel,
			teardown:     teardown,
		}

		// If the request is for a unary RPC, do the proxying inside the policy function.
		// Otherwise, we return the proxyRunner object and run it outside of the policy function, so it is not influenced by the resiliency policy's timeouts and retries. This way, clients are responsible for handling failures in streams, which could be very long-lived.
		if isStream {
			return &pr, nil
		}

		err = pr.run()
		if grpcDestinationAppID != "" {
			code := status.Code(err)
			diagnostics.DefaultMonitoring.ServiceInvocationResponseReceived(grpcDestinationAppID, int32(code), requestStartedAt)
		}

		if err != nil {
			// If the error is errRetryOnStreamingRPC, then that's permanent and should not cause the policy to retry
			if errors.Is(err, errRetryOnStreamingRPC) {
				err = backoff.Permanent(errRetryOnStreamingRPC)
			}
			return nil, err
		}
		return nil, nil
	})

	if cErr != nil {
		return cErr
	}

	// If the policy function returned a proxy runner, execute it here, outside of the policy function so it's not influenced by timeouts
	if pr != nil {
		cErr = pr.run()
		if cErr != nil {
			return cErr
		}
	}

	return nil
}

// Executes the proxying between client and server.
type proxyRunner struct {
	serverStream grpc.ServerStream
	clientStream grpc.ClientStream
	replayBuffer replayBufferCh
	headersSent  *atomic.Bool
	clientCtx    context.Context
	clientCancel func()
	teardown     func(bool)
}

// Performs the proxying.
func (r proxyRunner) run() error {
	defer r.dispose()

	// Explicitly *do not close* s2cErrChan and c2sErrChan, otherwise the select below will not terminate.
	// Channels do not have to be closed, it is just a control flow mechanism, see
	// https://groups.google.com/forum/#!msg/golang-nuts/pZwdYRGxCIk/qpbHxRRPJdUJ
	s2cErrChan := r.forwardServerToClient()
	c2sErrChan := r.forwardClientToServer()

	// We don't know which side is going to stop sending first, so we need a select between the two.
	for {
		select {
		case <-r.clientCtx.Done():
			// clientCtx is derived from outgoingCtx, so it's canceled when outgoingCtx is canceled too
			// Abort the request
			return status.FromContextError(r.clientCtx.Err()).Err()
		case s2cErr := <-s2cErrChan:
			if !errors.Is(s2cErr, io.EOF) {
				// We may have gotten a receive error (stream disconnected, a read error etc) in which case we need
				// to cancel the clientStream to the backend, let all of its goroutines be freed up by the CancelFunc and
				// exit with an error to the stack
				if _, ok := status.FromError(s2cErr); ok && s2cErr != nil {
					// If the error is already a gRPC one, return it as-is
					return s2cErr
				}
				return status.Error(codes.Internal, "failed proxying server-to-client: "+s2cErr.Error())
			}

			// This is the happy case where the sender has encountered io.EOF, and won't be sending anymore.
			// the clientStream>serverStream may continue pumping though.
			r.clientStream.CloseSend()
			continue
		case c2sErr := <-c2sErrChan:
			// This happens when the clientStream has nothing else to offer (io.EOF), returned a gRPC error. In those two
			// cases we may have received Trailers as part of the call. In case of other errors (stream closed) the trailers
			// will be nil.
			r.serverStream.SetTrailer(r.clientStream.Trailer())
			// c2sErr will contain RPC error from client code. If not io.EOF return the RPC error as server stream error.
			if !errors.Is(c2sErr, io.EOF) {
				return c2sErr
			}
			return nil
		}
	}
}

// Dispose of resources at the end.
func (r proxyRunner) dispose() {
	r.clientCancel()
	r.teardown(false)
}

func (r proxyRunner) forwardClientToServer() chan error {
	ret := make(chan error, 1)
	go func() {
		var err error
		f := &codec.Frame{}

		for r.clientStream.Context().Err() == nil && r.serverStream.Context().Err() == nil {
			err = r.clientStream.RecvMsg(f)
			if err != nil {
				ret <- err // this can be io.EOF which is happy case
				return
			}
			// In the case of retries, don't resend the headers.
			if r.headersSent.CompareAndSwap(false, true) {
				// This is a bit of a hack, but client to server headers are only readable after first client msg is
				// received but must be written to server stream before the first msg is flushed.
				// This is the only place to do it nicely.
				var md metadata.MD
				md, err = r.clientStream.Header()
				if err != nil {
					break
				}
				err = r.serverStream.SendHeader(md)
				if err != nil {
					break
				}
			}
			err = r.serverStream.SendMsg(f)
			if err != nil {
				break
			}
		}
	}()
	return ret
}

func (r proxyRunner) forwardServerToClient() chan error {
	ret := make(chan error, 1)
	go func() {
		var err error

		// Start by sending the buffered message if present
		if r.replayBuffer != nil {
			select {
			case msg := <-r.replayBuffer:
				err = r.clientStream.SendMsg(msg)
				// Re-add the message to the replay buffer
				r.replayBuffer <- msg
				if err != nil {
					ret <- err
					return
				}
			default:
				// nop - there's nothing in the buffer
			}
		}

		// Receive messages from the source stream and forward them to the destination stream
		for r.serverStream.Context().Err() == nil && r.clientStream.Context().Err() == nil {
			f := &codec.Frame{}
			err = r.serverStream.RecvMsg(f)

			if r.replayBuffer != nil {
				// We should never have more than one message in the replay buffer, otherwise it means that the user is trying to do retries with a streamed RPC and that's not supported
				select {
				case r.replayBuffer <- f:
					// nop
				default:
					if err == nil {
						err = errRetryOnStreamingRPC
					}
				}
			}

			if err != nil {
				ret <- err // this can be io.EOF which is happy case
				return
			}
			err = r.clientStream.SendMsg(f)
			if err != nil {
				break
			}
		}
	}()
	return ret
}
