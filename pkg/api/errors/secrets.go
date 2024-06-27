/*
 *
 * Copyright 2024 The Dapr Authors
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 * /
 */

package errors

import (
	"fmt"
	"net/http"

	"github.com/dapr/kit/errors"
	"google.golang.org/grpc/codes"
)

type SecretsStoreError struct {
	name             string
	skipResourceInfo bool
}

func SecretsStore(name string) *SecretsStoreError {
	return &SecretsStoreError{
		name: name,
	}
}

func (s *SecretsStoreError) NotConfigured() error {
	return s.build(
		errors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			fmt.Sprintf("secret store is not configured"),
			"ERR_SECRET_STORES_NOT_CONFIGURED",
		),
		"NOT_CONFIGURED",
	)
}

func (s *SecretsStoreError) PermissionDenied(key string) error {
	return s.build(
		errors.NewBuilder(
			codes.PermissionDenied,
			http.StatusForbidden,
			fmt.Sprintf("access denied by policy to get %q from %q", key, s.name),
			"ERR_PERMISSION_DENIED",
		),
		"PERMISSION_DENIED",
	)
}

func (s *SecretsStoreError) NotFound() error {
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusUnauthorized,
			fmt.Sprintf("failed finding secret store with key %s", s.name),
			"ERR_STORE_NOT_FOUND",
		),
		"NOT_FOUND",
	)
}

func (s *SecretsStoreError) GetSecret(key string, error string) error {
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("failed getting secret with key %s from secret store %s: %s", key, s.name, error),
			"ERR_GET_SECRET",
		),
		"GET_SECRET",
	)
}

func (s *SecretsStoreError) BulkSecretGet(error string) error {
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("failed getting secrets from secret store %s: %v", s.name, error),
			"ERR_GET_SECRET",
		),
		"GET_BULK_SECRET",
	)
}

func (s *SecretsStoreError) build(err *errors.ErrorBuilder, errCode string) error {
	if !s.skipResourceInfo {
		err = err.WithResourceInfo("secrets", s.name, "", "")
	}
	return err.
		WithErrorInfo(errors.CodePrefixSecretStore+errCode, nil).
		Build()
}
