// Based on https://github.com/trusch/grpc-proxy
// Copyright Michal Witkowski. Licensed under Apache2 license: https://github.com/trusch/grpc-proxy/blob/master/LICENSE.txt

package proxy

import (
	"context"
	"io"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

// Metadata header used to indicate if the call should be handled as a gRPC stream.
const StreamMetadataKey = "dapr-stream"

var log = logger.NewLogger("dapr.grpc.proxy")

var clientStreamDescForProxying = &grpc.StreamDesc{
	ServerStreams: true,
	ClientStreams: true,
}

// Error returned when the user is trying to make streaming RPCs and the Resiliency policy has retries enabled.
var errRetryOnStreamingRPC = status.Error(codes.FailedPrecondition, "cannot use resiliency policies with retries on streaming RPCs")

type replayBufferCh chan *codec.Frame

// RegisterService sets up a proxy handler for a particular gRPC service and method.
// The behaviour is the same as if you were registering a handler method, e.g. from a codegenerated pb.go file.
//
// This can *only* be used if the `server` also uses grpcproxy.CodecForServer() ServerOption.
func RegisterService(server *grpc.Server, director StreamDirector, resiliency resiliency.Provider, serviceName string, methodNames ...string) {
	streamer := &handler{
		director:   director,
		resiliency: resiliency,
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
func TransparentHandler(director StreamDirector, resiliency resiliency.Provider, isLocalFn func(string) (bool, error), connFactory DirectorConnectionFactory) grpc.StreamHandler {
	streamer := &handler{
		director:    director,
		resiliency:  resiliency,
		isLocalFn:   isLocalFn,
		connFactory: connFactory,
	}
	return streamer.handler
}

type handler struct {
	director    StreamDirector
	resiliency  resiliency.Provider
	isLocalFn   func(string) (bool, error)
	connFactory DirectorConnectionFactory
}

// handler is where the real magic of proxying happens.
// It is invoked like any gRPC server stream and uses the gRPC server framing to get and receive bytes from the wire,
// forwarding it to a ClientStream established against the relevant ClientConn.
func (s *handler) handler(srv any, serverStream grpc.ServerStream) error {
	// little bit of gRPC internals never hurt anyone
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
	if len(v) == 0 {
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	} else {
		isLocal, err := s.isLocalFn(v[0])
		if err == nil && !isLocal {
			policyDef = s.resiliency.EndpointPolicy(v[0], v[0]+":"+fullMethodName)
		} else {
			noOp := resiliency.NoOp{}
			policyDef = noOp.EndpointPolicy("", "")
		}
	}

	// When using resiliency, we need to put special care in handling proxied gRPC requests that are streams, because these can be long-lived.
	// - For unary gRPC calls, we need to apply the timeout and retry policies to the entire call, from start to end
	// - For streaming gRPC calls, timeouts and retries should only kick in during the initial "handshake". After that, the connection is to be considered established and we should continue with it until it's stopped or canceled or failed. Errors after the initial handshake should be sent directly to the client and server and not handled by Dapr.
	// With gRPC, every call is, at its core, a stream. The gRPC library maintains a list of which calls are to be interpreted as "unary" RPCs, and then "wraps them" so users can write code that behaves like a regular RPC without having to worry about underlying streams. This is possible by having knowledge of the proto files.
	// Because Dapr doesn't have the protos that are used for gRPC proxying, we cannot determine if a RPC is stream-based or "unary", so we can't do what the gRPC library does.
	// Instead, we're going to rely on the "dapr-stream" boolean header: if set, we consider the RPC as stream-based and apply Resiliency features (timeouts and retries) only to the initial handshake.
	var isStream bool
	v = md[StreamMetadataKey]
	if len(v) > 0 {
		isStream = utils.IsTruthy(v[0])
	}

	var replayBuffer replayBufferCh
	if !isStream {
		replayBuffer = make(replayBufferCh, 1)
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
	clientStreamOptSubtype := grpc.CallContentSubtype((&codec.Proxy{}).Name())
	headersSent := &atomic.Bool{}
	counter := atomic.Int32{}
	_, cErr := policyRunner(func(ctx context.Context) (struct{}, error) {
		// Get the current iteration count
		iter := counter.Add(1)

		// We require that the director's returned context inherits from background
		outgoingCtx, backendConn, target, teardown, err := s.director(context.Background(), fullMethodName)
		defer teardown(false)
		if err != nil {
			return struct{}{}, err
		}

		clientCtx, clientCancel := context.WithCancel(outgoingCtx)

		// (The next TODO comes from the original author of the library we adapted - leaving it here in case we want to do that for our own reasons)
		// TODO(mwitkow): Add a `forwarded` header to metadata, https://en.wikipedia.org/wiki/X-Forwarded-For.
		clientStream, err := grpc.NewClientStream(
			clientCtx,
			clientStreamDescForProxying,
			backendConn,
			fullMethodName,
			clientStreamOptSubtype,
		)
		if err != nil {
			code := status.Code(err)
			if target != nil && (code == codes.Unavailable || code == codes.Unauthenticated) {
				// It's possible that we get to this point while another goroutine is executing the same policy function.
				// For example, this could happen if this iteration has timed out and "policyRunner" has triggered a new execution already.
				// In this case, we should not teardown the connection because it could being used by the next execution. So just return and move on.
				if counter.Load() != iter {
					clientCancel()
					return struct{}{}, err
				}

				// Destroy the connection so it can be recreated
				teardown(true)

				// Re-connect
				backendConn, teardown, err = s.connFactory(outgoingCtx, target.Address, target.ID, target.Namespace)
				defer teardown(false)
				if err != nil {
					clientCancel()
					return struct{}{}, err
				}

				clientStream, err = grpc.NewClientStream(clientCtx, clientStreamDescForProxying, backendConn, fullMethodName, clientStreamOptSubtype)
				if err != nil {
					clientCancel()
					return struct{}{}, err
				}
			} else {
				clientCancel()
				return struct{}{}, err
			}
		}

		// Explicitly *do not close* s2cErrChan and c2sErrChan, otherwise the select below will not terminate.
		// Channels do not have to be closed, it is just a control flow mechanism, see
		// https://groups.google.com/forum/#!msg/golang-nuts/pZwdYRGxCIk/qpbHxRRPJdUJ
		s2cErrChan := forwardServerToClient(serverStream, clientStream, replayBuffer)
		c2sErrChan := forwardClientToServer(clientStream, serverStream, headersSent)

		// If the request is for a unary RPC, do the proxying here "synchronously".
		// Otherwise, we spawn a background goroutine, which is not influenced by the resiliency policy's timeout, and which doesn't return errors to the policy runners. This way, clients are responsible for handling failures in streams, which could be very long-lived.
		if isStream {
			go func() {
				defer clientCancel()

				for {
					select {
					case <-clientCtx.Done():
						log.Error("Proxied gRPC streaming request ended with client context error: " + clientCtx.Err().Error())
						return
					case <-outgoingCtx.Done():
						log.Error("Proxied gRPC streaming request ended with outgoing context error: " + outgoingCtx.Err().Error())
						return
					case s2cErr := <-s2cErrChan:
						if s2cErr != io.EOF {
							log.Error("Proxied gRPC streaming request ended with error on the server-to-client side: " + s2cErr.Error())
							return
						}
						// The sender has encountered io.EOF, and won't be sending anymore.
						// the clientStream>serverStream may continue pumping though.
						clientStream.CloseSend()
						continue
					case c2sErr := <-c2sErrChan:
						// This happens when the clientStream has nothing else to offer (io.EOF), returned a gRPC error. In those two
						// cases we may have received Trailers as part of the call. In case of other errors (stream closed) the trailers
						// will be nil.
						serverStream.SetTrailer(clientStream.Trailer())
						// c2sErr will contain RPC error from client code. If not io.EOF return the RPC error as server stream error.
						if c2sErr != io.EOF {
							log.Error("Proxied gRPC streaming request ended with error on the client-to-server side: " + c2sErr.Error())
						}
						return
					}
				}
			}()

			// Return with no error while the background goroutine processes the stream
			return struct{}{}, nil
		}

		defer clientCancel()

		// We don't know which side is going to stop sending first, so we need a select between the two.
		for {
			select {
			case <-clientCtx.Done():
				// Abort the request
				return struct{}{}, status.FromContextError(clientCtx.Err()).Err()
			case <-outgoingCtx.Done():
				// Abort the request
				return struct{}{}, status.FromContextError(outgoingCtx.Err()).Err()
			case s2cErr := <-s2cErrChan:
				if s2cErr != io.EOF {
					// We may have gotten a receive error (stream disconnected, a read error etc) in which case we need
					// to cancel the clientStream to the backend, let all of its goroutines be freed up by the CancelFunc and
					// exit with an error to the stack
					return struct{}{}, status.Error(codes.Internal, "failed proxying server-to-client: "+s2cErr.Error())
				}

				// This is the happy case where the sender has encountered io.EOF, and won't be sending anymore.
				// the clientStream>serverStream may continue pumping though.
				clientStream.CloseSend()
				continue
			case c2sErr := <-c2sErrChan:
				// This happens when the clientStream has nothing else to offer (io.EOF), returned a gRPC error. In those two
				// cases we may have received Trailers as part of the call. In case of other errors (stream closed) the trailers
				// will be nil.
				serverStream.SetTrailer(clientStream.Trailer())
				// c2sErr will contain RPC error from client code. If not io.EOF return the RPC error as server stream error.
				if c2sErr != io.EOF {
					return struct{}{}, c2sErr
				}
				return struct{}{}, nil
			}
		}
	})

	return cErr
}

func forwardClientToServer(src grpc.ClientStream, dst grpc.ServerStream, headersSent *atomic.Bool) chan error {
	ret := make(chan error, 1)
	go func() {
		var err error
		f := &codec.Frame{}

		for src.Context().Err() == nil && dst.Context().Err() == nil {
			err = src.RecvMsg(f)
			if err != nil {
				ret <- err // this can be io.EOF which is happy case
				return
			}
			// In the case of retries, don't resend the headers.
			if headersSent.CompareAndSwap(false, true) {
				// This is a bit of a hack, but client to server headers are only readable after first client msg is
				// received but must be written to server stream before the first msg is flushed.
				// This is the only place to do it nicely.
				var md metadata.MD
				md, err = src.Header()
				if err != nil {
					break
				}
				err = dst.SendHeader(md)
				if err != nil {
					break
				}
			}
			err = dst.SendMsg(f)
			if err != nil {
				break
			}
		}
	}()
	return ret
}

func forwardServerToClient(src grpc.ServerStream, dst grpc.ClientStream, replayBuffer replayBufferCh) chan error {
	ret := make(chan error, 1)
	go func() {
		var err error

		// Start by sending the buffered message if present
		if replayBuffer != nil {
			select {
			case msg := <-replayBuffer:
				err = dst.SendMsg(msg)
				if err != nil {
					ret <- err
					return
				}
			default:
				// nop - there's nothing in the buffer
			}
		}

		// Receive messages from the source stream and forward them to the destination stream
		for src.Context().Err() == nil && dst.Context().Err() == nil {
			f := &codec.Frame{}
			err = src.RecvMsg(f)

			if replayBuffer != nil {
				// We should never have more than one message in the replay buffer, otherwise it means that the user is trying to do retries with a streamed RPC and that's not supported
				select {
				case replayBuffer <- f:
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
			err = dst.SendMsg(f)
			if err != nil {
				break
			}
		}
	}()
	return ret
}
