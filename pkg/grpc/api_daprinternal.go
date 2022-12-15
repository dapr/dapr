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

package grpc

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Maximum size, in bytes, for the buffer used by CallLocalStream: 4KB.
const StreamBufferSize = 4 << 10

// CallLocal is used for internal dapr to dapr calls. It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	if a.appChannel == nil {
		return nil, status.Error(codes.Internal, messages.ErrChannelNotFound)
	}

	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}

	err = a.callLocalValidateACL(ctx, req)
	if err != nil {
		return nil, err
	}

	var callerAppID string
	callerIDHeader, ok := req.Metadata()[invokev1.CallerIDHeader]
	if ok && len(callerIDHeader.Values) > 0 {
		callerAppID = callerIDHeader.Values[0]
	} else {
		callerAppID = "unknown"
	}

	diag.DefaultMonitoring.ServiceInvocationRequestReceived(callerAppID, req.Message().Method)

	var statusCode int32
	defer func() {
		diag.DefaultMonitoring.ServiceInvocationResponseSent(callerAppID, req.Message().Method, statusCode)
	}()

	// stausCode will be read by the deferred method above
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		statusCode = int32(codes.Internal)
		return nil, status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
	} else {
		statusCode = resp.Status().Code
	}

	return resp.Proto(), nil
}

// CallLocalStream is a variant of CallLocal that uses gRPC streams to send data in chunks, rather than in an unary RPC.
// It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error { //nolint:nosnakecase
	if a.appChannel == nil {
		return status.Error(codes.Internal, messages.ErrChannelNotFound)
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	// Read the first chunk of the incoming request
	// This contains the metadata of the request
	chunk := &internalv1pb.InternalInvokeRequestStream{}
	err := stream.RecvMsg(chunk)
	if err != nil {
		return err
	}
	if chunk.Request == nil || chunk.Request.Metadata == nil || chunk.Request.Message == nil {
		return status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, "request does not contain the required fields in the leading chunk")
	}
	pr, pw := io.Pipe()
	req, err := invokev1.InternalInvokeRequest(chunk.Request)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}
	req.WithRawData(pr, chunk.Request.Message.ContentType)
	defer req.Close()

	err = a.callLocalValidateACL(ctx, req)
	if err != nil {
		return err
	}

	// Read the rest of the data in background as we submit the request
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

			done, readErr = messaging.ReadChunk(chunk, pw)
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

			if chunk.Request != nil && (chunk.Request.Metadata != nil || chunk.Request.Message != nil) {
				pw.CloseWithError(errors.New("request metadata found in non-leading chunk"))
				return
			}
		}

		pw.Close()
	}()

	// Submit the request to the app
	res, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		return status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
	}
	defer res.Close()

	// Respond to the caller
	r := res.RawData()
	resProto := res.Proto()
	buf := make([]byte, StreamBufferSize)
	var (
		proto *internalv1pb.InternalInvokeResponseStream
		n     int
	)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		proto = &internalv1pb.InternalInvokeResponseStream{
			Payload: &commonv1pb.StreamPayload{},
		}

		// First message only
		if resProto != nil {
			proto.Response = resProto
			resProto = nil
		}

		if r != nil {
			n, err = r.Read(buf)
			if err == io.EOF {
				proto.Payload.Complete = true
			} else if err != nil {
				return err
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
			return err
		}

		// Stop with the last chunk
		if proto.Payload.Complete {
			break
		}
	}

	return nil
}

// CallActor invokes a virtual actor.
func (a *api) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	req, err := invokev1.InternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}

	// We don't do resiliency here as it is handled in the API layer. See InvokeActor().
	resp, err := a.actor.Call(ctx, req)
	if err != nil {
		// We have to remove the error to keep the body, so callers must re-inspect for the header in the actual response.
		if errors.Is(err, actors.ErrDaprResponseHeader) {
			return resp.Proto(), nil
		}

		err = status.Errorf(codes.Internal, messages.ErrActorInvoke, err)
		return nil, err
	}
	return resp.Proto(), nil
}

// Used by CallLocal and CallLocalStream to check the request against the access control list
func (a *api) callLocalValidateACL(ctx context.Context, req *invokev1.InvokeMethodRequest) error {
	if a.accessControlList != nil {
		// An access control policy has been specified for the app. Apply the policies.
		operation := req.Message().Method
		var httpVerb commonv1pb.HTTPExtension_Verb //nolint:nosnakecase
		// Get the HTTP verb in case the application protocol is "http"
		if a.appProtocol == config.HTTPProtocol && req.Metadata() != nil && len(req.Metadata()) > 0 {
			httpExt := req.Message().GetHttpExtension()
			if httpExt != nil {
				httpVerb = httpExt.GetVerb()
			}
		}
		callAllowed, errMsg := acl.ApplyAccessControlPolicies(ctx, operation, httpVerb, a.appProtocol, a.accessControlList)

		if !callAllowed {
			return status.Errorf(codes.PermissionDenied, errMsg)
		}
	}

	return nil
}
