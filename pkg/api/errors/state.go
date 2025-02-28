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
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

type StateStoreError struct {
	name             string
	skipResourceInfo bool
}

func StateStore(name string) *StateStoreError {
	return &StateStoreError{
		name: name,
	}
}

func (s *StateStoreError) NotFound(appID string) error {
	msg := fmt.Sprintf("%s store %s is not found", metadata.StateStoreType, s.name)
	s.skipResourceInfo = true
	var meta map[string]string
	if len(appID) > 0 {
		meta = map[string]string{"appID": appID}
	}
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			errorcodes.StateStoreNotFound.Code,
			string(errorcodes.StateStoreNotFound.Category),
		),
		errorcodes.StateStoreNotFound.GrpcCode,
		meta,
	)
}

func (s *StateStoreError) NotConfigured(appID string) error {
	msg := fmt.Sprintf("%s store %s is not configured", metadata.StateStoreType, s.name)
	s.skipResourceInfo = true
	var meta map[string]string
	if len(appID) > 0 {
		meta = map[string]string{"appID": appID}
	}
	return s.build(
		errors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			msg,
			errorcodes.StateStoreNotConfigured.Code,
			string(errorcodes.StateStoreNotConfigured.Category),
		),
		errorcodes.StateStoreNotConfigured.GrpcCode,
		meta,
	)
}

func (s *StateStoreError) InvalidKeyName(key string, msg string) error {
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			errorcodes.StateMalformedRequest.Code,
			string(errorcodes.StateMalformedRequest.Category),
		).WithFieldViolation(key, msg),
		errorcodes.StateMalformedRequest.GrpcCode,
		nil,
	)
}

/**** Transactions ****/

func (s *StateStoreError) TransactionsNotSupported() error {
	return s.build(
		errors.NewBuilder(
			codes.Unimplemented,
			http.StatusInternalServerError,
			fmt.Sprintf("state store %s doesn't support transactions", s.name),
			errorcodes.StateStoreTransactionsNotSupported.Code,
			string(errorcodes.StateStoreTransactionsNotSupported.Category),
		).WithHelpLink("https://docs.dapr.io/reference/components-reference/supported-state-stores/", "Check the list of state stores and the features they support"),
		errorcodes.StateStoreTransactionsNotSupported.GrpcCode,
		nil,
	)
}

func (s *StateStoreError) TooManyTransactionalOps(count int, max int) error {
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", count, max),
			errorcodes.StateStoreTooManyTransactions.Code,
			string(errorcodes.StateStoreTooManyTransactions.Category),
		),
		errorcodes.StateStoreTooManyTransactions.GrpcCode,
		map[string]string{
			"currentOpsTransaction": strconv.Itoa(count),
			"maxOpsPerTransaction":  strconv.Itoa(max),
		},
	)
}

/**** Query API ****/

func (s *StateStoreError) QueryUnsupported() error {
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			"state store does not support querying",
			errorcodes.StateStoreQueryNotSupported.Code,
			string(errorcodes.StateStoreQueryNotSupported.Category),
		),
		errorcodes.StateStoreQueryNotSupported.GrpcCode,
		nil,
	)
}

func (s *StateStoreError) QueryFailed(detail string) error {
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("state store %s query failed: %s", s.name, detail),
			errorcodes.StateQuery.Code,
			string(errorcodes.StateQuery.Category),
		),
		errorcodes.StateQuery.GrpcCode,
		nil,
	)
}

func (s *StateStoreError) build(err *errors.ErrorBuilder, errCode string, metadata map[string]string) error {
	if !s.skipResourceInfo {
		err = err.WithResourceInfo("state", s.name, "", "")
	}
	return err.
		WithErrorInfo(errCode, metadata).
		Build()
}
