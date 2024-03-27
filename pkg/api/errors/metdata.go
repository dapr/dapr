package errors

import (
	"fmt"
    "net/http"
	"google.golang.org/grpc/codes"
	kiterrors "github.com/dapr/kit/errors"
)

func MetadataSubScriptionsError(name string) error {
	message := fmt.Sprintf("error returning subscriptions %s", name)
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message.
		"ERR_RETURNING_IN_SUBSCRIPTIONS_LIST",
	).
	WithErrorInfo(kiterrors.CodePrefix+"ERROR_RETURNING_LIST_OF_PUB/SUB_SUBSCRIPTIONS").
	WithErrorInfo(name, " ").
}

func MetadataInfoReturnError(name string) error {
	message := fmt.Sprintf("error returning metadata information")
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_METADATA_INFORMATION",
	).
	WithErrorInfo(kiterrors.CodePrefix)
	WithErrorInfo(name, " ").
}

func AppConnectionError(name string) error {
	message := fmt.Sprintf("error returning information related to connection to the app")
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_APP_CONNECTION",
	).
	WithErrorInfo(kiterrors.CodePrefix)
	WithErrorInfo(name, " ").
}

func EnableFeatureError(name string) error {
	message := fmt.Sprintf("error in listing features enabled")
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_ENABLE_FEATURE",
	).
	WithErrorInfo(kiterrors.CodePrefix)
	WithErrorInfo(name, " ").
}

func HttpEndpointsError(name string) error {
	message := fmt.Sprintf("error in providing name")
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_HTTP_ENDPOINTS"
	).
	WithErrorInfo(kiterrors.CodePrefix)
	WithErrorInfo(name, " ").
}