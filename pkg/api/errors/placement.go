/*
Copyright 2023 The Dapr Authors
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

	kiterrors "github.com/dapr/kit/errors"
)

const (
	PlacementServiceClosed         = "CLOSED"
	PlacementServiceAlreadyRunning = "ALREADY_RUNNING"

	// todo - Remove this placeholder. Use kiterrors prefix instead.
	CodePrefixPlacement = "DAPR_PLACEMENT_"
)

func PlacementServiceIsClosedOnRun(msg string) error {
	return kiterrors.NewBuilder(
		codes.Unavailable,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service unavailable: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixQueryFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixQueryFailed, nil).
		Build()
}

func PlacementServiceIsAlreadyRunning(msg string) error {
	return kiterrors.NewBuilder(
		codes.AlreadyExists,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service already running: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixQueryFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixQueryFailed, nil).
		Build()
}

func PlacementServiceContextError(msg string) error {
	return kiterrors.NewBuilder(
		codes.Unknown,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service encountered a context error: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixGetStateFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixGetStateFailed, nil).
		Build()
}

func PlacementServiceInternalError(msg string) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service encountered an internal error: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixGetStateFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixGetStateFailed, nil).
		Build()
}

func PlacementServiceUnAuthenticated(msg string) error {
	return kiterrors.NewBuilder(
		codes.Unauthenticated,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service encountered an authentication error: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixQueryFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixQueryFailed, nil).
		Build()
}

func PlacementServicePermissionDenied(msg string) error {
	return kiterrors.NewBuilder(
		codes.PermissionDenied,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service encountered a permission error: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixQueryFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixQueryFailed, nil).
		Build()
}

func PlacementServiceFailedPrecondition(msg string) error {
	return kiterrors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusExpectationFailed,
		fmt.Sprintf("Placement service encountered an precondition error: %s", msg),
		CodePrefixPlacement+kiterrors.CodePostfixQueryFailed,
	).WithErrorInfo(CodePrefixPlacement+kiterrors.CodePostfixQueryFailed, nil).
		Build()
}
