// Based on https://github.com/trusch/grpc-proxy
// Copyright Michal Witkowski. Licensed under Apache2 license: https://github.com/trusch/grpc-proxy/blob/master/LICENSE.txt

package proxy

import (
	"fmt"
	"io"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/grpc/proxy/codec"
	"github.com/dapr/dapr/pkg/resiliency"
)

var clientStreamDescForProxying = &grpc.StreamDesc{
	ServerStreams: true,
	ClientStreams: true,
}

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
		HandlerType: (*interface{})(nil),
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
func TransparentHandler(director StreamDirector, resiliency resiliency.Provider, isLocalFn func(string) (bool, error)) grpc.StreamHandler {
	streamer := &handler{
		director:   director,
		resiliency: resiliency,
		isLocalFn:  isLocalFn,
	}
	return streamer.handler
}

type handler struct {
	director      StreamDirector
	resiliency    resiliency.Provider
	isLocalFn     func(string) (bool, error)
	bufferedCalls []interface{}
}

// handler is where the real magic of proxying happens.
// It is invoked like any gRPC server stream and uses the gRPC server framing to get and receive bytes from the wire,
// forwarding it to a ClientStream established against the relevant ClientConn.
func (s *handler) handler(srv interface{}, serverStream grpc.ServerStream) error {
	// Clear the buffered calls on a new request.
	s.bufferedCalls = []interface{}{}

	// little bit of gRPC internals never hurt anyone
	fullMethodName, ok := grpc.MethodFromServerStream(serverStream)
	if !ok {
		return status.Errorf(codes.Internal, "lowLevelServerStream not exists in context")
	}

	// Fetch the AppId so we can reference it for resiliency.
	md, _ := metadata.FromIncomingContext(serverStream.Context())
	v := md.Get(diagnostics.GRPCProxyAppIDKey)

	// The app id check is handled in the StreamDirector. If we don't have it here, we just use a NoOp policy since we know the request is impossible.
	var policy resiliency.Runner
	if len(v) == 0 {
		noOp := resiliency.NoOp{}
		policy = noOp.EndpointPolicy(serverStream.Context(), "", "")
	} else {
		isLocal, err := s.isLocalFn(v[0])

		if err == nil && isLocal {
			policy = s.resiliency.EndpointPolicy(serverStream.Context(), v[0], fmt.Sprintf("%s:%s", v[0], fullMethodName))
		} else {
			noOp := resiliency.NoOp{}
			policy = noOp.EndpointPolicy(serverStream.Context(), "", "")
		}
	}

	cErr := policy(func(ctx context.Context) (rErr error) {
		// We require that the director's returned context inherits from the serverStream.Context().
		outgoingCtx, backendConn, teardown, err := s.director(serverStream.Context(), fullMethodName)
		defer teardown()
		if err != nil {
			return err
		}

		clientCtx, clientCancel := context.WithCancel(outgoingCtx)

		// TODO(mwitkow): Add a `forwarded` header to metadata, https://en.wikipedia.org/wiki/X-Forwarded-For.
		clientStream, err := grpc.NewClientStream(clientCtx, clientStreamDescForProxying, backendConn, fullMethodName, grpc.CallContentSubtype((&codec.Proxy{}).Name()))
		if err != nil {
			return err
		}

		// Explicitly *do not close* s2cErrChan and c2sErrChan, otherwise the select below will not terminate.
		// Channels do not have to be closed, it is just a control flow mechanism, see
		// https://groups.google.com/forum/#!msg/golang-nuts/pZwdYRGxCIk/qpbHxRRPJdUJ
		s2cErrChan := s.forwardServerToClient(serverStream, clientStream)
		c2sErrChan := s.forwardClientToServer(clientStream, serverStream)
		// We don't know which side is going to stop sending first, so we need a select between the two.
		for i := 0; i < 2; i++ {
			select {
			case s2cErr := <-s2cErrChan:
				if s2cErr == io.EOF {
					// this is the happy case where the sender has encountered io.EOF, and won't be sending anymore./
					// the clientStream>serverStream may continue pumping though.
					clientStream.CloseSend()
					continue
				} else {
					// however, we may have gotten a receive error (stream disconnected, a read error etc) in which case we need
					// to cancel the clientStream to the backend, let all of its goroutines be freed up by the CancelFunc and
					// exit with an error to the stack
					clientCancel()
					return status.Errorf(codes.Internal, "failed proxying s2c: %v", s2cErr)
				}
			case c2sErr := <-c2sErrChan:
				// This happens when the clientStream has nothing else to offer (io.EOF), returned a gRPC error. In those two
				// cases we may have received Trailers as part of the call. In case of other errors (stream closed) the trailers
				// will be nil.
				serverStream.SetTrailer(clientStream.Trailer())
				// c2sErr will contain RPC error from client code. If not io.EOF return the RPC error as server stream error.
				if c2sErr != io.EOF {
					return c2sErr
				}
				return nil
			}
		}
		return status.Errorf(codes.Internal, "gRPC proxying should never reach this stage.")
	})
	s.bufferedCalls = []interface{}{}
	return cErr
}

func (s *handler) forwardClientToServer(src grpc.ClientStream, dst grpc.ServerStream) chan error {
	ret := make(chan error, 1)
	go func() {
		f := &codec.Frame{}
		for i := 0; ; i++ {
			if err := src.RecvMsg(f); err != nil {
				ret <- err // this can be io.EOF which is happy case
				break
			}
			if i == 0 {
				// This is a bit of a hack, but client to server headers are only readable after first client msg is
				// received but must be written to server stream before the first msg is flushed.
				// This is the only place to do it nicely.
				md, err := src.Header()
				if err != nil {
					break
				}
				if err := dst.SendHeader(md); err != nil {
					break
				}
			}
			if err := dst.SendMsg(f); err != nil {
				break
			}
		}
	}()
	return ret
}

func (s *handler) forwardServerToClient(src grpc.ServerStream, dst grpc.ClientStream) chan error {
	ret := make(chan error, 1)
	go func() {
		f := &codec.Frame{}
		bufferedSends := s.bufferedCalls
		if len(bufferedSends) > 0 {
			for _, msg := range s.bufferedCalls {
				if err := dst.SendMsg(msg); err != nil {
					ret <- err
					return
				}
			}
		}
		for i := 0; ; i++ {
			if err := src.RecvMsg(f); err != nil {
				ret <- err // this can be io.EOF which is happy case
				break
			}
			s.bufferedCalls = append(s.bufferedCalls, f)
			if err := dst.SendMsg(f); err != nil {
				break
			}
		}
	}()
	return ret
}
