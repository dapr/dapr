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
	"fmt"
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/dapr/kit/errors"
)

func StateStoreNotConfigured() error {
	return errors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		"state store is not configured",
		"ERR_STATE_STORE_NOT_CONFIGURED",
	).
		WithErrorInfo(errors.CodePrefixStateStore+errors.CodeNotConfigured, nil).
		Build()
}

func StateStoreNotFound(storeName string) error {
	return errors.NewBuilder(
		codes.InvalidArgument, // TODO We should change this at some point. It should be codes.NotFound, but it will be a breaking change.
		http.StatusBadRequest,
		fmt.Sprintf("state store %s is not found", storeName),
		"ERR_STATE_STORE_NOT_FOUND",
	).
		WithErrorInfo(errors.CodePrefixStateStore+errors.CodeNotFound, nil).
		Build()
}

func StateStoreInvalidKeyName(storeName string, key string, msg string) error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_MALFORMED_REQUEST",
	).WithErrorInfo(errors.CodePrefixStateStore+errors.CodeIllegalKey, nil).
		WithResourceInfo("state", storeName, "", "").
		WithFieldViolation(key, msg).
		Build()
}

/**** Transactions ****/

func StateStoreTransactionsNotSupported(storeName string) error {
	return errors.NewBuilder(
		codes.Unimplemented,
		http.StatusInternalServerError,
		fmt.Sprintf(MsgStateTransactionsNotSupported, storeName),
		"ERR_STATE_STORE_NOT_SUPPORTED", // TODO: @elena-kolevska this is misleading and also used for different things ("query unsupported"); it should be removed in the next major version
	).
		WithErrorInfo(errors.CodePrefixStateStore+"TRANSACTIONS_NOT_SUPPORTED", nil).
		WithResourceInfo("state", storeName, "", "").
		WithHelpLink("https://docs.dapr.io/reference/components-reference/supported-state-stores/", "Check the list of state stores and the features they support").
		Build()
}

func StateStoreTooManyTransactionalOps(storeName string, count int, max int) error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", count, max),
		"ERR_STATE_STORE_TOO_MANY_TRANSACTIONS",
	).
		WithErrorInfo(errors.CodePrefixStateStore+"TOO_MANY_TRANSACTIONS", map[string]string{
			"currentOpsTransaction": strconv.Itoa(count),
			"maxOpsPerTransaction":  strconv.Itoa(max),
		}).
		WithResourceInfo("state", storeName, "", "").
		Build()
}

/**** Query API ****/

func StateStoreQueryUnsupported(storeName string) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"state store does not support querying",
		"ERR_STATE_STORE_NOT_SUPPORTED",
	).
		WithErrorInfo(errors.CodePrefixStateStore+"QUERYING_"+errors.CodeNotSupported, nil).
		WithResourceInfo("state", storeName, "", "").
		Build()
}

func StateStoreQueryFailed(storeName string, detail string) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("state store %s query failed: %s", storeName, detail),
		"ERR_STATE_QUERY",
	).
		WithErrorInfo(errors.CodePrefixStateStore+errors.CodePostfixQueryFailed, nil).
		WithResourceInfo("state", storeName, "", "").
		Build()
}
