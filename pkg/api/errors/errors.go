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

	kiterrors "github.com/dapr/kit/errors"
)

const (
	InFixName     ReasonSegment = "NAME_"
	InFixNegative ReasonSegment = "NEGATIVE_"
	PostFixName   ReasonSegment = "NAME"
	PostFixEmpty  ReasonSegment = "EMPTY"
)

func NotFound(name string, componentType string, metadata map[string]string, grpcCode codes.Code, httpCode int, legacyTag string, reason string) error {
	message := fmt.Sprintf("%s %s is not found", componentType, name)

	return kiterrors.NewBuilder(
		grpcCode,
		httpCode,
		message,
		legacyTag,
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func NotConfigured(name string, componentType string, metadata map[string]string, grpcCode codes.Code, httpCode int, legacyTag string, reason string) error {
	message := componentType + " " + name + " is not configured"

	return kiterrors.NewBuilder(
		grpcCode,
		httpCode,
		message,
		legacyTag,
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func Empty(name string, metadata map[string]string, reason string) error {
	message := name + " is empty"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func IncorrectNegative(name string, metadata map[string]string, reason string) error {
	message := name + " cannot be negative"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(reason, metadata).
		Build()
}
