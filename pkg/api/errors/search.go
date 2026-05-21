/*
Copyright 2026 The Dapr Authors
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

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

type SearchStoreError struct {
	name             string
	skipResourceInfo bool
}

func SearchStore(name string) *SearchStoreError {
	return &SearchStoreError{name: name}
}

func (s *SearchStoreError) NotFound() error {
	s.skipResourceInfo = true
	return s.build(
		kiterrors.NewBuilder(
			codes.NotFound,
			http.StatusNotFound,
			fmt.Sprintf("%s store %s is not found", metadata.SearchType, s.name),
			errorcodes.SearchStoreNotFound.Code,
			string(errorcodes.SearchStoreNotFound.Category),
		),
		errorcodes.SearchStoreNotFound.GrpcCode,
		nil,
	)
}

func (s *SearchStoreError) NotConfigured() error {
	s.skipResourceInfo = true
	return s.build(
		kiterrors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			fmt.Sprintf("%s store %s is not configured", metadata.SearchType, s.name),
			errorcodes.SearchStoreNotConfigured.Code,
			string(errorcodes.SearchStoreNotConfigured.Category),
		),
		errorcodes.SearchStoreNotConfigured.GrpcCode,
		nil,
	)
}

func (s *SearchStoreError) InvalidRequest(field string, msg string) error {
	return s.build(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			errorcodes.SearchInvalidRequest.Code,
			string(errorcodes.SearchInvalidRequest.Category),
		).WithFieldViolation(field, msg),
		errorcodes.SearchInvalidRequest.GrpcCode,
		nil,
	)
}

func (s *SearchStoreError) MissingField(field string) error {
	msg := fmt.Sprintf("missing required field %s", field)
	return s.InvalidRequest(field, msg)
}

func (s *SearchStoreError) IndexNotFound(index string) error {
	return s.build(
		kiterrors.NewBuilder(
			codes.NotFound,
			http.StatusNotFound,
			fmt.Sprintf("search index %s is not found in store %s", index, s.name),
			errorcodes.SearchIndexNotFound.Code,
			string(errorcodes.SearchIndexNotFound.Category),
		),
		errorcodes.SearchIndexNotFound.GrpcCode,
		map[string]string{"index": index},
	)
}

func (s *SearchStoreError) OperationFailed(operation string, err error) error {
	return s.build(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("search store %s %s failed: %s", s.name, operation, err),
			errorcodes.SearchOperationFailed.Code,
			string(errorcodes.SearchOperationFailed.Category),
		),
		errorcodes.SearchOperationFailed.GrpcCode,
		map[string]string{"operation": operation, "error": err.Error()},
	)
}

func (s *SearchStoreError) build(err *kiterrors.ErrorBuilder, errCode string, meta map[string]string) error {
	if !s.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.SearchType), s.name, "", "")
	}
	return err.WithErrorInfo(errCode, meta).Build()
}
