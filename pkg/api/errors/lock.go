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

// LockStoreError is a struct that holds the error information for lock store operations.
type LockStoreError struct {
	name string
}

// LockStore creates a new LockStoreError instance.
func LockStore(name string) *LockStoreError {
	return &LockStoreError{
		name: name,
	}
}

// NotConfigured returns an error indicating that lock stores are not configured.
func (l *LockStoreError) NotConfigured() error {
	return kiterrors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		"lock store is not configured",
		"",
		string(errorcodes.LockStoreNotConfigured.Category),
	).
		WithErrorInfo(errorcodes.LockStoreNotConfigured.Code, nil).
		Build()
}

// NotFound returns an error indicating that the lock store was not found.
func (l *LockStoreError) NotFound() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("lock store %s not found", l.name),
		"",
		string(errorcodes.LockStoreNotFound.Category),
	).
		WithErrorInfo(errorcodes.LockStoreNotFound.Code, map[string]string{"storeName": l.name}).
		Build()
}

// ResourceIDEmpty returns an error indicating that the resource ID is empty.
func (l *LockStoreError) ResourceIDEmpty() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("ResourceId is empty in lock store %s", l.name),
		"",
		string(errorcodes.CommonMalformedRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonMalformedRequest.Code, map[string]string{"storeName": l.name}).
		Build()
}

// LockOwnerEmpty returns an error indicating that the lock owner is empty.
func (l *LockStoreError) LockOwnerEmpty() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("LockOwner is empty in lock store %s", l.name),
		"",
		string(errorcodes.CommonMalformedRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonMalformedRequest.Code, map[string]string{"storeName": l.name}).
		Build()
}

// ExpiryInSecondsNotPositive returns an error indicating that the expiry time is not positive.
func (l *LockStoreError) ExpiryInSecondsNotPositive() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("ExpiryInSeconds is not positive in lock store %s", l.name),
		"",
		string(errorcodes.CommonMalformedRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonMalformedRequest.Code, map[string]string{"storeName": l.name}).
		Build()
}

// TryLockFailed returns an error indicating that the lock acquisition failed.
func (l *LockStoreError) TryLockFailed(detail string) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("failed to try acquiring lock: %s", detail),
		"",
		string(errorcodes.LockTry.Category),
	).
		WithErrorInfo(errorcodes.LockTry.Code, map[string]string{"storeName": l.name}).
		Build()
}

// UnlockFailed returns an error indicating that the unlock operation failed.
func (l *LockStoreError) UnlockFailed(detail string) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("failed to release lock: %s", detail),
		"",
		string(errorcodes.LockUnlock.Category),
	).
		WithErrorInfo(errorcodes.LockUnlock.Code, map[string]string{"storeName": l.name}).
		Build()
}
