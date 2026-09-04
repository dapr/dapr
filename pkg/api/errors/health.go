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

func HealthNotReady(targets string) error {
	msg := "dapr is not ready"
	if targets != "" {
		msg = fmt.Sprintf("dapr is not ready: %s", targets)
	}
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"",
		string(errorcodes.HealthNotReady.Category),
	).
		WithErrorInfo(errorcodes.HealthNotReady.Code, nil).
		Build()
}

func HealthAppIDNotMatch() error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr app-id does not match",
		"",
		string(errorcodes.HealthAppidNotMatch.Category),
	).
		WithErrorInfo(errorcodes.HealthAppidNotMatch.Code, nil).
		Build()
}

func HealthOutboundNotReady() error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr outbound is not ready",
		"",
		string(errorcodes.HealthOutboundNotReady.Category),
	).
		WithErrorInfo(errorcodes.HealthOutboundNotReady.Code, nil).
		Build()
}
