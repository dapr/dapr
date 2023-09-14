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

package actors

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type ActorError struct {
	body        []byte
	headers     invokev1.DaprInternalMetadata
	contentType string
	statusCode  int
	message     string
}

func NewActorError(invokeResponse *invokev1.InvokeMethodResponse) *ActorError {
	if invokeResponse == nil {
		return &ActorError{
			body:        []byte{},
			headers:     nil,
			contentType: "text/plain",
			statusCode:  500,
			message:     "actor error without response body",
		}
	}

	body, err := invokeResponse.RawDataFull()
	if err != nil {
		return &ActorError{
			body:        []byte{},
			headers:     nil,
			contentType: "text/plain",
			statusCode:  500,
			message:     fmt.Sprintf("could not read error's body: %s", err),
		}
	}

	statusCode := int(invokeResponse.Status().Code)
	if !invokeResponse.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}

	return &ActorError{
		body:        body,
		headers:     invokeResponse.Headers(),
		contentType: invokeResponse.ContentType(),
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

func AsActorError(err error) (*ActorError, bool) {
	var actorError *ActorError
	if errors.As(err, &actorError) {
		return actorError, true
	}

	return nil, false
}

func IsActorError(err error) bool {
	var actorError *ActorError
	return errors.As(err, &actorError)
}
