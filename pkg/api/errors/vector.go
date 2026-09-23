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

// NotFound reports a store name that does not resolve while other vector
// stores are configured.
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

// NotConfigured reports a store lookup made when no vector store is
// configured at all.
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

// InvalidRequest reports request validation the runtime performs before
// invoking the provider: keyed-upsert IDs that are empty or duplicated, an
// IndexingOptionsAlpha1 combination the proposal forbids, a vector query that
// does not set exactly one of vector / by_id, and a collection created
// without dimensions.
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

// MissingField is InvalidRequest for a required field left unset.
func (v *VectorStoreError) MissingField(field string) error {
	msg := fmt.Sprintf("missing required field %s", field)
	return v.InvalidRequest(field, msg)
}

// CollectionNotFound is reserved for a missing collection the runtime can
// attribute itself. A NOT_FOUND raised by the provider is passed through
// instead, so that the component's own message and details survive.
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

// OperationFailed reports a runtime-side failure while carrying out an
// operation. It must not be used to wrap a component error: doing so would
// discard the canonical code and ErrorInfo the caller relies on.
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
