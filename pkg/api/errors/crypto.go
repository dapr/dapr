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

func CryptoProvidersNotConfigured() error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"crypto providers not configured",
		errorcodes.CryptoProvidersNotConfigured.Code,
		string(errorcodes.CryptoProvidersNotConfigured.Category),
	).
		WithErrorInfo(errorcodes.CryptoProvidersNotConfigured.Code, nil).
		Build()
}

func CryptoProviderNotFound(name string) error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("crypto provider %s not found", name),
		errorcodes.CryptoProviderNotFound.Code,
		string(errorcodes.CryptoProviderNotFound.Category),
	).
		WithErrorInfo(errorcodes.CryptoProviderNotFound.Code, map[string]string{"provider": name}).
		Build()
}

func CryptoProviderNameEmpty() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"invalid request: missing component name",
		errorcodes.CommonBadRequest.Code,
		string(errorcodes.CommonBadRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonBadRequest.Code, nil).
		Build()
}

func CryptoOperation(msg string, err error) error {
	var detail string
	switch {
	case msg != "" && err != nil:
		detail = fmt.Sprintf("%s: %v", msg, err)
	case err != nil:
		detail = err.Error()
	default:
		detail = msg
	}
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("failed to perform operation: %s", detail),
		errorcodes.Crypto.Code,
		string(errorcodes.Crypto.Category),
	).
		WithErrorInfo(errorcodes.Crypto.Code, nil).
		Build()
}

func CryptoBadRequest(msg string) error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("invalid request: %s", msg),
		errorcodes.CommonBadRequest.Code,
		string(errorcodes.CommonBadRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonBadRequest.Code, nil).
		Build()
}
