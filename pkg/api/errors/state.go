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

	grpcCodes "google.golang.org/grpc/codes"

	kitErrors "github.com/dapr/kit/errors"
)

func StateStoreNotConfigured() *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.FailedPrecondition,
		http.StatusInternalServerError,
		"state store is not configured",
		"ERR_STATE_STORE_NOT_CONFIGURED",
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+kitErrors.CodeNotConfigured, nil)
}

func StateStoreNotFound(storeName string) *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.InvalidArgument, // TODO check if it was used in the past, we should change it. It should be grpcCodes.NotFound
		http.StatusBadRequest,
		fmt.Sprintf("state store %s is not found", storeName),
		"ERR_STATE_STORE_NOT_FOUND",
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+kitErrors.CodeNotFound, nil)
}

func StateStoreInvalidKeyName(storeName string, key string, msg string) *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_MALFORMED_REQUEST",
	).WithErrorInfo(kitErrors.CodePrefixStateStore+kitErrors.CodeIllegalKey, nil).
		WithResourceInfo("state", storeName, "", "").
		WithFieldViolation(key, msg)
}

/**** Transactions ****/

func StateStoreTransactionsNotSupported(storeName string) *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.Unimplemented,
		http.StatusInternalServerError,
		fmt.Sprintf(kitErrors.MsgStateTransactionsNotSupported, storeName),
		"ERR_STATE_STORE_NOT_SUPPORTED", // TODO: @elena-kolevska this is misleading and also used for different things ("query unsupported"); it should be removed in the next major version
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+"TRANSACTIONS_NOT_SUPPORTED", nil).
		WithResourceInfo("state", storeName, "", "").
		WithHelpLink("https://docs.dapr.io/reference/components-reference/supported-state-stores/", "Check the list of state stores and the features they support")
}

func StateStoreTooManyTransactionalOps(count int, max int) *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", count, max),
		"ERR_STATE_STORE_TOO_MANY_TRANSACTIONS",
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+"TOO_MANY_TRANSACTIONS", map[string]string{
			"currentOpsTransaction": strconv.Itoa(count),
			"maxOpsPerTransaction":  strconv.Itoa(max),
		}).
		WithResourceInfo("state", "", "", "")
}

/**** Query API ****/

func StateStoreQueryUnsupported(storeName string) *kitErrors.Error {
	return kitErrors.New(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		"state store does not support querying",
		"ERR_STATE_STORE_NOT_SUPPORTED",
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+"QUERYING_"+kitErrors.CodeNotSupported, nil).
		WithResourceInfo("state", storeName, "", "")
}

func StateStoreQueryFailed(storeName string, detail string) *kitErrors.Error {
	// TODO: @elena-kolevska #change-in-next-major: The http and grpc codes are wrong, but they're legacy.
	return kitErrors.New(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("state store %s query failed: %s", storeName, detail),
		"ERR_STATE_QUERY",
	).
		WithErrorInfo(kitErrors.CodePrefixStateStore+kitErrors.CodePostfixQueryFailed, nil).
		WithResourceInfo("state", storeName, "", "")
}
