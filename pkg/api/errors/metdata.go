package errors

import (
	"fmt"
    "net/http"
	"google.golang.org/grpc/codes"
	kiterrors "github.com/dapr/kit/errors"
)

func IncomingContextMetadataNotFound() error {
	message := "metadata not found in the incoming context"
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_METADATA_NOT_FOUND"
	).
	WithErrorInfo("METADATA_NOT_FOUND", nil).
	WithResourceInfo("", "", message).
	Build()
}

func MetadataFromIncomingContextFailed() error {
	message := "failed to retrieve metadata from incoming context"
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument
        http.StatusBadRequest,
		message,
		"ERR_METADATA_RETRIEVAL_FAILED",
	).
	WithErrorInfo("METADATA_RETRIEVAL_FAILED", nil).
	WithResourceInfo("", "", "", message).
	Build()
}