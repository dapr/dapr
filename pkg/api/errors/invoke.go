package errors

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/kit/errors"
)

type InvokeError struct {
	name string
}

type InvokeMetadataError struct {
	i                *InvokeError
	metadata         map[string]string
	skipResourceInfo bool
}

// this belongs in the dapr/kit repo?
const codePrefixServiceInvocation = "DAPR_SERVICE_INVOCATION_"

func (i *InvokeError) WithAppError(appID string, err error) *InvokeMetadataError {
	var meta map[string]string
	if len(appID) > 0 {
		meta = map[string]string{
			"appID": appID,
		}
	}
	if err != nil {
		meta["error"] = err.Error()
	}

	return &InvokeMetadataError{
		i:        i,
		metadata: meta,
	}
}

func Invoke(name string) *InvokeError {
	return &InvokeError{
		name: name,
	}
}

func (i *InvokeMetadataError) DirectInvoke() error {
	msg := fmt.Sprintf("failed to invoke, id: %s, err: %v", i.metadata["appID"], i.metadata["error"])
	return i.build(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"ERR_DIRECT_INVOKE",
		"INVOKE_ERROR",
	)
}

func (i *InvokeMetadataError) MalformedResponse() error {
	msg := "code not found in response"
	return i.build(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"ERR_MALFORMED_RESPONSE",
		"MALFORMED_RESPONSE",
	)
}

func (i *InvokeMetadataError) NoPermission() error {
	msg := "you don't have enough permission"
	return i.build(
		codes.PermissionDenied,
		http.StatusForbidden,
		msg,
		"ERR_DIRECT_INVOKE",
		"NO_PERMISSION",
	)
}

func (i *InvokeMetadataError) NoAppID() error {
	msg := "failed getting app id either from the URL path or the header dapr-app-id"
	return i.build(
		codes.NotFound,
		http.StatusNotFound,
		msg,
		"ERR_DIRECT_INVOKE",
		"APP_ID_EMPTY",
	)
}

func (i *InvokeMetadataError) NotReady() error {
	msg := fmt.Sprintf("Invoke API is not ready, id: %s", i.metadata["appID"])
	return i.build(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		"ERR_DIRECT_INVOKE",
		"NOT_READY",
	)
}

func (i *InvokeMetadataError) build(grpcCode codes.Code, httpCode int, msg, tag, errCode string) error {
	err := errors.NewBuilder(grpcCode, httpCode, msg, tag).WithHelpLink("https://docs.dapr.io/developing-applications/building-blocks/service-invocation/howto-invoke-discover-services/", "Check the details of Service Invocation")
	if !i.skipResourceInfo {
		err.WithResourceInfo("invoke", i.i.name, "", msg)
	}
	return err.WithErrorInfo(codePrefixServiceInvocation+errCode, i.metadata).Build()
}

func ErrorToKitError(err error) *errors.Error {
	if kitError, ok := errors.FromError(err); ok {
		return kitError
	}
	return nil
}
