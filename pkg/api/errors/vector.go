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

type VectorStoreError struct {
	name             string
	skipResourceInfo bool
}

func VectorStore(name string) *VectorStoreError {
	return &VectorStoreError{name: name}
}

func (v *VectorStoreError) NotFound() error {
	v.skipResourceInfo = true
	return v.build(
		kiterrors.NewBuilder(
			codes.NotFound,
			http.StatusNotFound,
			fmt.Sprintf("%s store %s is not found", metadata.VectorType, v.name),
			errorcodes.VectorStoreNotFound.Code,
			string(errorcodes.VectorStoreNotFound.Category),
		),
		errorcodes.VectorStoreNotFound.GrpcCode,
		nil,
	)
}

func (v *VectorStoreError) NotConfigured() error {
	v.skipResourceInfo = true
	return v.build(
		kiterrors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			fmt.Sprintf("%s store %s is not configured", metadata.VectorType, v.name),
			errorcodes.VectorStoreNotConfigured.Code,
			string(errorcodes.VectorStoreNotConfigured.Category),
		),
		errorcodes.VectorStoreNotConfigured.GrpcCode,
		nil,
	)
}

func (v *VectorStoreError) InvalidRequest(field string, msg string) error {
	return v.build(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			errorcodes.VectorInvalidRequest.Code,
			string(errorcodes.VectorInvalidRequest.Category),
		).WithFieldViolation(field, msg),
		errorcodes.VectorInvalidRequest.GrpcCode,
		nil,
	)
}

func (v *VectorStoreError) MissingField(field string) error {
	msg := fmt.Sprintf("missing required field %s", field)
	return v.InvalidRequest(field, msg)
}

func (v *VectorStoreError) CollectionNotFound(collection string) error {
	return v.build(
		kiterrors.NewBuilder(
			codes.NotFound,
			http.StatusNotFound,
			fmt.Sprintf("vector collection %s is not found in store %s", collection, v.name),
			errorcodes.VectorCollectionNotFound.Code,
			string(errorcodes.VectorCollectionNotFound.Category),
		),
		errorcodes.VectorCollectionNotFound.GrpcCode,
		map[string]string{"collection": collection},
	)
}

func (v *VectorStoreError) OperationFailed(operation string, err error) error {
	return v.build(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("vector store %s %s failed: %s", v.name, operation, err),
			errorcodes.VectorOperationFailed.Code,
			string(errorcodes.VectorOperationFailed.Category),
		),
		errorcodes.VectorOperationFailed.GrpcCode,
		map[string]string{"operation": operation, "error": err.Error()},
	)
}

func (v *VectorStoreError) build(err *kiterrors.ErrorBuilder, errCode string, meta map[string]string) error {
	if !v.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.VectorType), v.name, "", "")
	}
	return err.WithErrorInfo(errCode, meta).Build()
}
