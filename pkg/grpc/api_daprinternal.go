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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

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

	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		statusCode = int32(codes.Internal)
		err = status.Errorf(codes.Internal, messages.ErrChannelInvoke, err)
		return nil, err
	} else {
		statusCode = resp.Status().Code
	}

	return resp.Proto(), err
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
