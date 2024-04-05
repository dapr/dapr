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

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/kit/errors"
)

type LockError struct {
	name  string
	owner string
}

type LockMetadataError struct {
	l                *LockError
	metadata         map[string]string
	skipResourceInfo bool
}

func Lock(name string, owner string) *LockError {
	return &LockError{
		name:  name,
		owner: owner,
	}
}

func (l *LockError) WithMetadata(metadata map[string]string) *LockMetadataError {
	return &LockMetadataError{
		l:        l,
		metadata: metadata,
	}
}

func (l *LockError) WithAppError(appID string, err error) *LockMetadataError {
	meta := map[string]string{
		"appID": appID,
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	return &LockMetadataError{
		l:        l,
		metadata: meta,
	}
}

func (l *LockMetadataError) ResourceIDEmpty() error {
	return l.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"lock resource id is empty",
		"ERR_MALFORMED_REQUEST",
		"RESOURCE_ID_EMPTY",
	)
}

func (l *LockMetadataError) LockOwnerEmpty() error {
	return l.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"lock owner is empty",
		"ERR_MALFORMED_REQUEST",
		"OWNER_EMPTY",
	)
}

func (l *LockMetadataError) ExpiryInSecondsNotPositive() error {
	return l.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"expiry in seconds is not positive",
		"ERR_MALFORMED_REQUEST",
		"EXPIRY_NOT_POSITIVE",
	)
}

func (l *LockMetadataError) TryLockFailed() error {
	return l.build(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to try acquiring lock",
		"ERR_TRY_LOCK",
		"TRY_LOCK_FAILED",
	)
}

func (l *LockMetadataError) UnlockFailed() error {
	return l.build(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to release lock",
		"ERR_UNLOCK",
		"UNLOCK_FAILED",
	)
}

func (l *LockMetadataError) NotFound() error {
	l.skipResourceInfo = true
	return l.build(
		codes.InvalidArgument,
		http.StatusBadRequest,// TODO: change this to be http.StatusNotFound in 2 releases since it would be a breaking change to the errors/lock.go
		fmt.Sprintf("%s %s is not found", metadata.LockStoreType, l.l.name),
		"ERR_LOCK_STORE_NOT_FOUND",
		errors.CodeNotFound,
	)
}

func (l *LockMetadataError) NotConfigured() error {
	l.skipResourceInfo = true
	return l.build(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		fmt.Sprintf("%s %s is not configured", metadata.LockStoreType, l.l.name),
		"ERR_LOCK_STORE_NOT_CONFIGURED",
		errors.CodeNotConfigured,
	)
}

func (l *LockMetadataError) build(grpcCode codes.Code, httpCode int, msg, tag, errCode string) error {
	err := errors.NewBuilder(grpcCode, httpCode, msg, tag)
	if !l.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.LockStoreType), l.l.name, l.l.owner, msg)
	}
	return err.WithErrorInfo(
		errors.CodePrefixLock+errCode,
		l.metadata,
	).Build()
}
