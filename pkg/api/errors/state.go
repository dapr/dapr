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
			"ERR_STATE_STORE_NOT_FOUND",
		),
		errors.CodeNotFound,
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
			"ERR_STATE_STORE_NOT_CONFIGURED",
		),
		errors.CodeNotConfigured,
		meta,
	)
}

func (s *StateStoreError) InvalidKeyName(key string, msg string) error {
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			"ERR_MALFORMED_REQUEST",
		).WithFieldViolation(key, msg),
		errors.CodeIllegalKey,
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
			"ERR_STATE_STORE_NOT_SUPPORTED", // TODO: @elena-kolevska this code misleading and also used for different things ("query unsupported"); it should be removed in the next major version
		).WithHelpLink("https://docs.dapr.io/reference/components-reference/supported-state-stores/", "Check the list of state stores and the features they support"),
		"TRANSACTIONS_NOT_SUPPORTED",
		nil,
	)
}

func (s *StateStoreError) TooManyTransactionalOps(count int, max int) error {
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", count, max),
			"ERR_STATE_STORE_TOO_MANY_TRANSACTIONS",
		),
		"TOO_MANY_TRANSACTIONS",
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
			"ERR_STATE_STORE_NOT_SUPPORTED",
		),
		"QUERYING_"+errors.CodeNotSupported,
		nil,
	)
}

func (s *StateStoreError) QueryFailed(detail string) error {
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("state store %s query failed: %s", s.name, detail),
			"ERR_STATE_QUERY",
		),
		errors.CodePostfixQueryFailed,
		nil,
	)
}

func (s *StateStoreError) build(err *errors.ErrorBuilder, errCode string, metadata map[string]string) error {
	if !s.skipResourceInfo {
		err = err.WithResourceInfo("state", s.name, "", "")
	}
	return err.
		WithErrorInfo(errors.CodePrefixStateStore+errCode, metadata).
		Build()
}
