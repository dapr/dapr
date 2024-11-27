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

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/kit/errors"
	"google.golang.org/grpc/codes"
)

type SecretStoreError struct {
	name             string
	skipResourceInfo bool
}

func SecretStore(name string) *SecretStoreError {
	return &SecretStoreError{
		name: name,
	}
}

func (s *SecretStoreError) NotConfigured() error {
	msg := fmt.Sprintf("secret store is not configured")
	s.skipResourceInfo = true
	return s.build(
		errors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			msg,
			"ERR_SECRET_STORES_NOT_CONFIGURED",
		),
		errors.CodeNotConfigured,
		nil,
	)
}

func (s *SecretStoreError) NotFound() error {
	msg := fmt.Sprintf("secret store %s not found", s.name)
	s.skipResourceInfo = true
	return s.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusUnauthorized,
			msg,
			"ERR_SECRET_STORE_NOT_FOUND",
		),
		errors.CodeNotFound,
		nil,
	)
}

func (s *SecretStoreError) PermissionDenied(key string) error {
	msg := fmt.Sprintf("access denied by policy to get %q from %q", key, s.name)
	return s.build(
		errors.NewBuilder(
			codes.PermissionDenied,
			http.StatusForbidden,
			msg,
			"ERR_PERMISSION_DENIED",
		),
		"PERMISSION_DENIED",
		nil,
	)
}

func (s *SecretStoreError) GetSecret(key string, err error) error {
	msg := fmt.Sprintf("failed getting secret with key %s from secret store %s: %s", key, s.name, err.Error())
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			msg,
			"ERR_SECRET_GET",
		),
		"GET_SECRET",
		nil,
	)
}

func (s *SecretStoreError) BulkSecretGet(err error) error {
	msg := fmt.Sprintf("failed getting secrets from secret store %s: %v", s.name, err.Error())
	return s.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			msg,
			"ERR_SECRET_GET",
		),
		"GET_BULK_SECRET",
		nil,
	)
}

func (s *SecretStoreError) build(err *errors.ErrorBuilder, errCode string, meta map[string]string) error {
	if !s.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.SecretStoreType), s.name, "", "")
	}
	return err.
		WithErrorInfo(errors.CodePrefixSecretStore+errCode, meta).
		Build()
}
