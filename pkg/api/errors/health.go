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
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

type HealthError struct{}

func Health() *HealthError {
	return &HealthError{}
}

func (h *HealthError) NotReady(targets []string) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("dapr is not ready: [%s]", strings.Join(targets, " ")),
		errorcodes.HealthNotReady.Code,
		string(errorcodes.HealthNotReady.Category),
	).
		WithErrorInfo(errorcodes.HealthNotReady.GrpcCode, nil).
		Build()
}

func (h *HealthError) AppIDNotMatch() error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr app-id does not match",
		errorcodes.HealthAppidNotMatch.Code,
		string(errorcodes.HealthAppidNotMatch.Category),
	).
		WithErrorInfo(errorcodes.HealthAppidNotMatch.GrpcCode, nil).
		Build()
}

func (h *HealthError) OutboundNotReady() error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr outbound is not ready",
		errorcodes.HealthOutboundNotReady.Code,
		string(errorcodes.HealthOutboundNotReady.Category),
	).
		WithErrorInfo(errorcodes.HealthOutboundNotReady.GrpcCode, nil).
		Build()
}
