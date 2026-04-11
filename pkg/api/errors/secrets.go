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
	"github.com/dapr/kit/errors"
)

type SecretStoreError struct {
	storeName string
}

func SecretStore(storeName string) *SecretStoreError {
	return &SecretStoreError{storeName: storeName}
}

// NotConfigured returns an error when no secret stores have been configured.
func (s *SecretStoreError) NotConfigured() error {
	return errors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		"secret store is not configured",
		errorcodes.SecretStoreNotConfigured.Code,
		string(errorcodes.SecretStoreNotConfigured.Category),
	).
		WithErrorInfo(errorcodes.SecretStoreNotConfigured.GrpcCode, nil).
		Build()
}

// NotFound returns an error when the named secret store does not exist.
func (s *SecretStoreError) NotFound() error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusUnauthorized,
		fmt.Sprintf("failed finding secret store with key %s", s.storeName),
		errorcodes.SecretStoreNotFound.Code,
		string(errorcodes.SecretStoreNotFound.Category),
	).
		WithErrorInfo(errorcodes.SecretStoreNotFound.GrpcCode, nil).
		Build()
}

// PermissionDenied returns an error when policy denies access to the secret.
func (s *SecretStoreError) PermissionDenied(key string) error {
	return errors.NewBuilder(
		codes.PermissionDenied,
		http.StatusForbidden,
		fmt.Sprintf("access denied by policy to get %q from %q", key, s.storeName),
		errorcodes.SecretPermissionDenied.Code,
		string(errorcodes.SecretPermissionDenied.Category),
	).
		WithErrorInfo(errorcodes.SecretPermissionDenied.GrpcCode, nil).
		Build()
}

// GetFailed returns an error when fetching a single secret fails.
func (s *SecretStoreError) GetFailed(key string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("failed getting secret with key %s from secret store %s: %s", key, s.storeName, err.Error()),
		errorcodes.SecretGet.Code,
		string(errorcodes.SecretGet.Category),
	).
		WithErrorInfo(errorcodes.SecretGet.GrpcCode, nil).
		Build()
}

// BulkGetFailed returns an error when fetching all secrets from a store fails.
func (s *SecretStoreError) BulkGetFailed(err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("failed getting secrets from secret store %s: %v", s.storeName, err),
		errorcodes.SecretGet.Code,
		string(errorcodes.SecretGet.Category),
	).
		WithErrorInfo(errorcodes.SecretGet.GrpcCode, nil).
		Build()
}
