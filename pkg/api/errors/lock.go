/*
Copyright 2022 The Dapr Authors
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

	kitErrors "github.com/dapr/kit/errors"
	grpcCodes "google.golang.org/grpc/codes"
)

const (
	PostFixIDEmpty                    = "RESOURCE_ID_EMPTY"
	PostFixLockOwnerEmpty             = "LOCK_OWNER_EMPTY"
	PostFixExpiryInSecondsNotPositive = "NEGATIVE_EXPIRY"
	PostFixTryLock                    = "TRY_LOCK"
	PostFixUnlock                     = "UNLOCK"
)

func DistributedLockResourceIDEmpty(storeName, owner string) error {
	msg := fmt.Sprintf("ResourceId is empty in lock store %s", storeName)
	return kitErrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_RESOURCE_ID_EMPTY",
	).
		WithErrorInfo(kitErrors.CodePrefixLock+PostFixIDEmpty, nil).
		WithResourceInfo("lock", storeName, owner, "").
		Build()
}

func DistributedLockOwnerEmpty(storeName string) error {
	msg := fmt.Sprintf("LockOwner is empty in lock store %s", storeName)
	return kitErrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_LOCK_OWNER_EMPTY",
	).
		WithErrorInfo(kitErrors.CodePrefixLock+PostFixLockOwnerEmpty, nil).
		WithResourceInfo("lock", storeName, "", "").
		Build()
}

func DistributedLockExpiryNotPositive(storeName, owner string) error {
	msg := fmt.Sprintf("ExpiryInSeconds is not positive in lock store %s", storeName)
	return kitErrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_EXPIRY_NOT_POSITIVE",
	).
		WithErrorInfo(kitErrors.CodePrefixLock+PostFixExpiryInSecondsNotPositive, nil).
		WithResourceInfo("lock", storeName, owner, "").
		Build()
}

func DistributedTryLockFailed(storeName, owner string) error {
	msg := fmt.Sprintf("failed to try acquiring lock in lock store %s", storeName)
	return kitErrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		msg,
		"ERR_TRY_LOCK",
	).
		WithErrorInfo(kitErrors.CodePrefixLock+PostFixTryLock, nil).
		WithResourceInfo("lock", storeName, owner, "").
		Build()
}

func DistributedUnlockFailed(storeName, owner string) error {
	msg := fmt.Sprintf("failed to release lock in lock store %s", storeName)
	return kitErrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		msg,
		"ERR_UNLOCK",
	).
		WithErrorInfo(kitErrors.CodePrefixLock+PostFixUnlock, nil).
		WithResourceInfo("lock", storeName, owner, "").
		Build()
}
