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

func ActorTypeReserved(actorType string) error {
	return kiterrors.NewBuilder(
		codes.PermissionDenied,
		http.StatusForbidden,
		fmt.Sprintf("actor type %q is reserved for the Dapr workflow runtime; direct actor API access is not permitted", actorType),
		"",
		string(errorcodes.ActorTypeReserved.Category),
	).
		WithErrorInfo(errorcodes.ActorTypeReserved.Code, map[string]string{"actorType": actorType}).
		Build()
}

func ActorTimerCreate(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error creating actor timer: %s", err),
		"",
		string(errorcodes.ActorTimerCreate.Category),
	).
		WithErrorInfo(errorcodes.ActorTimerCreate.Code, nil).
		Build()
}

func ActorReminderCreate(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error creating actor reminder: %s", err),
		"",
		string(errorcodes.ActorReminderCreate.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderCreate.Code, nil).
		Build()
}

func ActorReminderDelete(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error deleting actor reminder: %s", err),
		"",
		string(errorcodes.ActorReminderDelete.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderDelete.Code, nil).
		Build()
}

func ActorReminderGet(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error getting actor reminder: %s", err),
		"",
		string(errorcodes.ActorReminderGet.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderGet.Code, nil).
		Build()
}

func ActorReminderNotFound(name string) error {
	return kiterrors.NewBuilder(
		codes.NotFound,
		http.StatusNotFound,
		fmt.Sprintf("actor reminder not found: %s", name),
		"",
		string(errorcodes.ActorReminderNotFound.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderNotFound.Code, map[string]string{"name": name}).
		Build()
}

func ActorReminderAlreadyExists(name string) error {
	return kiterrors.NewBuilder(
		codes.AlreadyExists,
		http.StatusConflict,
		fmt.Sprintf("actor reminder already exists: %s", name),
		"",
		string(errorcodes.ActorReminderAlreadyExists.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderAlreadyExists.Code, map[string]string{"name": name}).
		Build()
}

func ActorReminderNonHosted() error {
	return kiterrors.NewBuilder(
		codes.PermissionDenied,
		http.StatusForbidden,
		"operations on actor reminders are only possible on hosted actor types",
		"",
		string(errorcodes.ActorReminderNonHosted.Category),
	).
		WithErrorInfo(errorcodes.ActorReminderNonHosted.Code, nil).
		Build()
}

func ActorMalformedRequest(v any) error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("failed deserializing HTTP body: %v", v),
		"",
		string(errorcodes.CommonMalformedRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonMalformedRequest.Code, nil).
		Build()
}

func ActorBadRequest(v any) error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("invalid request: %v", v),
		"",
		string(errorcodes.CommonBadRequest.Category),
	).
		WithErrorInfo(errorcodes.CommonBadRequest.Code, nil).
		Build()
}

func ActorStateTransactionSave(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error saving actor transaction state: %s", err),
		"",
		string(errorcodes.ActorStateTransactionSave.Category),
	).
		WithErrorInfo(errorcodes.ActorStateTransactionSave.Code, nil).
		Build()
}

func ActorStateGet(err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error getting actor state: %s", err),
		"",
		string(errorcodes.ActorStateGet.Category),
	).
		WithErrorInfo(errorcodes.ActorStateGet.Code, nil).
		Build()
}

func ActorInvoke(v any) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error invoke actor method: %s", v),
		"",
		string(errorcodes.ActorInvokeMethod.Category),
	).
		WithErrorInfo(errorcodes.ActorInvokeMethod.Code, nil).
		Build()
}
