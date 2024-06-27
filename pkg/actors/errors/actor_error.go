/*
Copyright 2023 The Dapr Authors
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
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// ActorError is an error returned by an Actor via a header + body in the method's response.
type ActorError struct {
	body        []byte
	headers     map[string]*internalv1pb.ListStringValue
	contentType string
	statusCode  int
	message     string
}

func NewActorError(res *internalv1pb.InternalInvokeResponse) error {
	if res == nil {
		return fmt.Errorf("could not parse actor error: no response object")
	}

	statusCode := int(res.GetStatus().GetCode())
	if !res.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}

	msg := res.GetMessage()
	return &ActorError{
		body:        msg.GetData().GetValue(),
		headers:     res.GetHeaders(),
		contentType: msg.GetContentType(),
		statusCode:  statusCode,
		message:     "actor error with details in body",
	}
}

func (e *ActorError) Error() string {
	return e.message
}

func (e *ActorError) Headers() invokev1.DaprInternalMetadata {
	return e.headers
}

func (e *ActorError) ContentType() string {
	return e.contentType
}

func (e *ActorError) StatusCode() int {
	return e.statusCode
}

func (e *ActorError) Body() []byte {
	return e.body
}

func As(err error) (*ActorError, bool) {
	var actorError *ActorError
	if errors.As(err, &actorError) {
		return actorError, true
	}

	return nil, false
}

func Is(err error) bool {
	var actorError *ActorError
	return errors.As(err, &actorError)
}
