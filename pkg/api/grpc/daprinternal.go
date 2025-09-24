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
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/acl"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// CallLocal is used for internal dapr to dapr calls. It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	appChannel := a.channels.AppChannel()
	if appChannel == nil {
		return nil, status.Error(codes.Internal, messages.ErrChannelNotFound)
	}

	req, err := invokev1.FromInternalInvokeRequest(in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}
	defer req.Close()

	// Check the ACL
	err = a.callLocalValidateACL(ctx, req)
	if err != nil {
		return nil, err
	}

	// Diagnostics
	callerAppID := a.callLocalRecordRequest(req.Proto())

	var statusCode int32
	defer func() {
		diag.DefaultMonitoring.ServiceInvocationResponseSent(callerAppID, statusCode)
	}()

	// stausCode will be read by the deferred method above
	res, err := appChannel.InvokeMethod(ctx, req, "")
	if err != nil {
		statusCode = int32(codes.Internal)
		return nil, status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
	} else {
		statusCode = res.Status().GetCode()
	}
	defer res.Close()

	return res.ProtoWithData()
}

// CallLocalStream is a variant of CallLocal that uses gRPC streams to send data in chunks, rather than in an unary RPC.
// It is invoked by another Dapr instance with a request to the local app.
func (a *api) CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error { //nolint:nosnakecase
	appChannel := a.channels.AppChannel()
	if appChannel == nil {
		return status.Error(codes.Internal, messages.ErrChannelNotFound)
	}

	// Read the first chunk of the incoming request
	// This contains the metadata of the request
	chunk := &internalv1pb.InternalInvokeRequestStream{}
	err := stream.RecvMsg(chunk)
	if err != nil {
		return err
	}
	if chunk.GetRequest().GetMetadata() == nil || chunk.GetRequest().GetMessage() == nil {
		return status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, "request does not contain the required fields in the leading chunk")
	}

	// Append the invoked method to the context's metadata so we can use it for tracing
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		md[diagConsts.DaprCallLocalStreamMethodKey] = []string{chunk.GetRequest().GetMessage().GetMethod()}
	}

	// Create the request object
	// The "rawData" of the object will be a pipe to which content is added chunk-by-chunk
	pr, pw := io.Pipe()
	req, err := invokev1.FromInternalInvokeRequest(chunk.GetRequest())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, messages.ErrInternalInvokeRequest, err.Error())
	}
	req.WithRawData(pr).
		WithContentType(chunk.GetRequest().GetMessage().GetContentType())
	defer req.Close()

	// If the data has a type_url, set that in the object too
	// This is necessary to support the gRPC->gRPC service invocation (legacy, non-proxy) path correctly
	// (Note that GetTypeUrl could return an empty value, so this call becomes a no-op)
	req.WithDataTypeURL(chunk.GetRequest().GetMessage().GetData().GetTypeUrl())

	ctx := stream.Context()

	// Check the ACL
	err = a.callLocalValidateACL(ctx, req)
	if err != nil {
		return err
	}

	// Diagnostics
	callerAppID := a.callLocalRecordRequest(req.Proto())

	var statusCode int32
	defer func() {
		diag.DefaultMonitoring.ServiceInvocationResponseSent(callerAppID, statusCode)
	}()

	// Read the rest of the data in background as we submit the request
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

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
				readSeq, readErr = messaging.ReadChunk(payload, pw)
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

			if chunk.GetRequest().GetMetadata() != nil || chunk.GetRequest().GetMessage() != nil {
				pw.CloseWithError(errors.New("request metadata found in non-leading chunk"))
				return
			}
		}

		pw.Close()
	}()

	// Submit the request to the app
	res, err := appChannel.InvokeMethod(ctx, req, "")
	if err != nil {
		statusCode = int32(codes.Internal)
		return status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
	}
	if res == nil {
		statusCode = int32(codes.Internal)
		return status.Errorf(codes.Internal, messages.ErrChannelInvoke, errors.New("no response received from stream"))
	}

	defer res.Close()
	statusCode = res.Status().GetCode()

	// Respond to the caller
	buf := invokev1.BufPool.Get().(*[]byte)
	defer func() {
		invokev1.BufPool.Put(buf)
	}()
	r := res.RawData()
	resProto := res.Proto()

	// If there's a message in the proto, we remove it from the message we send to avoid sending it twice
	// We re-add it when the method ends to ensure we can perform retries
	messageData := resProto.GetMessage().GetData()
	messageDataValue := messageData.GetValue()
	if len(messageDataValue) > 0 {
		messageData.Value = nil
		defer func() {
			messageData.Value = messageDataValue
		}()
	}

	proto := &internalv1pb.InternalInvokeResponseStream{}
	var (
		n    int
		seq  uint64
		done bool
	)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// First message only - add the response
		if resProto != nil {
			proto.Response = resProto
			resProto = nil
		} else {
			// Reset the object so we can re-use it
			proto.Reset()
		}

		if r != nil {
			n, err = r.Read(*buf)
			if errors.Is(err, io.EOF) {
				done = true
			} else if err != nil {
				return err
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
		if proto.GetResponse() != nil || proto.GetPayload() != nil {
			err = stream.SendMsg(proto)
			if err != nil {
				return fmt.Errorf("error sending message: %w", err)
			}
		}

		// Stop with the last chunk
		// This will make the method return and close the stream
		if done {
			break
		}
	}

	return nil
}

// CallActor invokes a virtual actor.
func (a *api) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	// We don't do resiliency here as it is handled in the API layer. See InvokeActor().
	var res *internalv1pb.InternalInvokeResponse
	router, err := a.ActorRouter(ctx)
	if err == nil {
		if in.Metadata == nil {
			in.Metadata = make(map[string]*internalv1pb.ListStringValue)
		}
		in.Metadata["X-Dapr-Remote"] = &internalv1pb.ListStringValue{Values: []string{"true"}}
		res, err = router.Call(ctx, in)
	}

	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}

		actorErr, isActorErr := actorerrors.As(err)
		if res != nil && isActorErr {
			// We have to remove the error to keep the body, so callers must re-inspect for the header in the actual response.
			res.Message.Data = &anypb.Any{
				Value: actorErr.Body(),
			}
			res.Headers = actorErr.Headers()
			return res, nil
		}

		return nil, messages.ErrActorInvoke.WithFormat(err)
	}
	return res, nil
}

// CallActorReminder invokes an internal virtual actor.
func (a *api) CallActorReminder(ctx context.Context, in *internalv1pb.Reminder) (*emptypb.Empty, error) {
	router, err := a.ActorRouter(ctx)
	if err != nil {
		return nil, err
	}

	period, _ := actorapi.NewReminderPeriod(in.GetPeriod())
	return nil, router.CallReminder(ctx, &actorapi.Reminder{
		Name:           in.GetName(),
		ActorType:      in.GetActorType(),
		ActorID:        in.GetActorId(),
		Data:           in.GetData(),
		DueTime:        in.GetDueTime(),
		Period:         period,
		ExpirationTime: in.GetExpirationTime().AsTime(),
		IsTimer:        in.GetIsTimer(),
		IsRemote:       true,
		SkipLock:       in.GetSkipLock(),
	})
}

func (a *api) CallActorStream(req *internalv1pb.InternalInvokeRequest, stream internalv1pb.ServiceInvocation_CallActorStreamServer) error {
	router, err := a.ActorRouter(stream.Context())
	if err != nil {
		return err
	}

	if req.Metadata == nil {
		req.Metadata = make(map[string]*internalv1pb.ListStringValue)
	}
	req.Metadata["X-Dapr-Remote"] = &internalv1pb.ListStringValue{Values: []string{"true"}}

	err = router.CallStream(stream.Context(), req, func(req *internalv1pb.InternalInvokeResponse) (bool, error) {
		return false, stream.Send(req)
	})
	if err != nil {
		return err
	}

	return nil
}

// Used by CallLocal and CallLocalStream to check the request against the access control list
func (a *api) callLocalValidateACL(ctx context.Context, req *invokev1.InvokeMethodRequest) error {
	if a.accessControlList != nil {
		// An access control policy has been specified for the app. Apply the policies.
		operation := req.Message().GetMethod()
		var httpVerb commonv1pb.HTTPExtension_Verb //nolint:nosnakecase
		// Get the HTTP verb in case the application protocol is "http"
		appProtocolIsHTTP := a.Universal.AppConnectionConfig().Protocol.IsHTTP()
		if appProtocolIsHTTP && req.Metadata() != nil && len(req.Metadata()) > 0 {
			httpExt := req.Message().GetHttpExtension()
			if httpExt != nil {
				httpVerb = httpExt.GetVerb()
			}
		}
		callAllowed, errMsg := acl.ApplyAccessControlPolicies(ctx, operation, httpVerb, appProtocolIsHTTP, a.accessControlList)

		if !callAllowed {
			return status.Error(codes.PermissionDenied, errMsg)
		}
	}

	return nil
}

// Internal function that records the received request for diagnostics
// After invoking this method, make sure to `defer` a call like:
//
// ```go
// var statusCode int32
// defer func() {
// diag.DefaultMonitoring.ServiceInvocationResponseSent(callerAppID, req.Message().Method, statusCode)
// }()
// ```
func (a *api) callLocalRecordRequest(req *internalv1pb.InternalInvokeRequest) (callerAppID string) {
	callerIDHeader, ok := req.GetMetadata()[invokev1.CallerIDHeader]
	if ok && len(callerIDHeader.GetValues()) > 0 {
		callerAppID = callerIDHeader.GetValues()[0]
	} else {
		callerAppID = "unknown"
	}

	diag.DefaultMonitoring.ServiceInvocationRequestReceived(callerAppID)

	return
}
