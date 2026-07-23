/*
Copyright 2024 The Dapr Authors
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

package errors

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

// ServiceInvocationError builds standardized errors for the Service Invocation API.
type ServiceInvocationError struct {
	appID string
}

// ServiceInvocation returns a ServiceInvocationError scoped to the given target app ID.
func ServiceInvocation(appID string) *ServiceInvocationError {
	return &ServiceInvocationError{appID: appID}
}

// NotReady returns a standardized error when the direct messaging client is not initialized.
func (s *ServiceInvocationError) NotReady() error {
	msg := "invoke API is not ready"
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.ServiceInvocationNotReady.Code,
		string(errorcodes.ServiceInvocationNotReady.Category),
	).
		WithErrorInfo(errorcodes.ServiceInvocationNotReady.GrpcCode, nil).
		Build()
}

// NoAppID returns a standardized error when the target app ID is missing from the request.
func (s *ServiceInvocationError) NoAppID() error {
	msg := "failed getting app id either from the URL path or the header dapr-app-id"
	return kiterrors.NewBuilder(
		codes.NotFound,
		http.StatusNotFound,
		msg,
		errorcodes.ServiceInvocationNoAppID.Code,
		string(errorcodes.ServiceInvocationNoAppID.Category),
	).
		WithErrorInfo(errorcodes.ServiceInvocationNoAppID.GrpcCode, nil).
		Build()
}

// InvokeFailed returns a standardized error for a failed direct service invocation.
func (s *ServiceInvocationError) InvokeFailed(err error) error {
	msg := fmt.Sprintf("failed to invoke, id: %s, err: %v", s.appID, err)
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.ServiceInvocationDirectInvoke.Code,
		string(errorcodes.ServiceInvocationDirectInvoke.Category),
	).
		WithResourceInfo("service-invocation", s.appID, "", msg).
		WithErrorInfo(errorcodes.ServiceInvocationDirectInvoke.GrpcCode, map[string]string{
			"appID": s.appID,
		}).
		Build()
}
