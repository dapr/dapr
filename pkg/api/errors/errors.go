package errors

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"

	kiterrors "github.com/dapr/kit/errors"
)

const (
	InFixName     = "NAME_"
	InFixNegative = "NEGATIVE_"
	PostFixName   = "NAME"
	PostFixEmpty  = "EMPTY"
)

func NotFound(name string, componentType string, metadata map[string]string, grpcCode codes.Code, httpCode int, legacyTag string, reason string) error {
	message := fmt.Sprintf("%s %s is not found", componentType, name)

	return kiterrors.NewBuilder(
		grpcCode,
		httpCode,
		message,
		legacyTag,
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func NotConfigured(name string, componentType string, metadata map[string]string, grpcCode codes.Code, httpCode int, legacyTag string, reason string) error {
	message := fmt.Sprintf("%s %s is not configured", componentType, name)

	return kiterrors.NewBuilder(
		grpcCode,
		httpCode,
		message,
		legacyTag,
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func Empty(name string, metadata map[string]string, reason string) error {
	message := name + " is empty"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(reason, metadata).
		Build()
}

func IncorrectNegative(name string, metadata map[string]string, reason string) error {
	message := name + " cannot be negative"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(reason, metadata).
		Build()
}
