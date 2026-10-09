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

type SecretStoreError struct {
	name string
}

func SecretStore(name string) *SecretStoreError {
	return &SecretStoreError{name: name}
}

func (s *SecretStoreError) NotConfigured() error {
	return kiterrors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		"secret store is not configured",
		"",
		string(errorcodes.SecretStoreNotConfigured.Category),
	).
		WithErrorInfo(errorcodes.SecretStoreNotConfigured.Code, nil).
		Build()
}

func (s *SecretStoreError) NotFound() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusUnauthorized,
		fmt.Sprintf("failed finding secret store with key %s", s.name),
		"",
		string(errorcodes.SecretStoreNotFound.Category),
	).
		WithErrorInfo(errorcodes.SecretStoreNotFound.Code, map[string]string{"store": s.name}).
		Build()
}

func (s *SecretStoreError) PermissionDenied(key string) error {
	return kiterrors.NewBuilder(
		codes.PermissionDenied,
		http.StatusForbidden,
		fmt.Sprintf("access denied by policy to get %q from %q", key, s.name),
		"",
		string(errorcodes.SecretPermissionDenied.Category),
	).
		WithErrorInfo(errorcodes.SecretPermissionDenied.Code, map[string]string{"store": s.name, "key": key}).
		Build()
}

func (s *SecretStoreError) Get(key string, err error) error {
	msg := fmt.Sprintf("failed getting secret with key %s from secret store %s", key, s.name)
	if err != nil {
		msg = fmt.Sprintf("%s: %s", msg, err.Error())
	}
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"",
		string(errorcodes.SecretGet.Category),
	).
		WithErrorInfo(errorcodes.SecretGet.Code, map[string]string{"store": s.name, "key": key}).
		Build()
}

func (s *SecretStoreError) BulkGet(err error) error {
	msg := fmt.Sprintf("failed getting secrets from secret store %s", s.name)
	if err != nil {
		msg = fmt.Sprintf("%s: %v", msg, err)
	}
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"",
		string(errorcodes.SecretGet.Category),
	).
		WithErrorInfo(errorcodes.SecretGet.Code, map[string]string{"store": s.name}).
		Build()
}
