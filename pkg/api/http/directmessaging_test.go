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

//nolint:goconst
package http

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestV1DirectMessagingEndpoints(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	compStore := compstore.New()
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		directMessaging: mockDirectMessaging,
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints(), nil)

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging without querystring for external invocation - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		// Double slash added on purpose
		apiPath := "v1.0//invoke/http://api.github.com/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging without querystring for external invocation again - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/http://123.45.67.89:3000/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://123.45.67.89:3000"
				}),
				mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
					msg := req.Message()
					if msg.GetMethod() != "fakeMethod" {
						return false
					}
					if msg.GetHttpExtension().GetVerb() != commonv1.HTTPExtension_POST {
						return false
					}
					return true
				}),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging without querystring - 201 Created", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponseWithStatusCode(http.StatusCreated)
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 201, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging without querystring for external invocation - 201 Created", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponseWithStatusCode(http.StatusCreated)
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 201, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with dapr-app-id in header - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "dapr-app-id", "fakeAppID")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with headers for external invocation - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "Authorization", "token sometoken", "Accept-Language", "en-US")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with dapr-app-id in basic auth - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.doRequest("dapr-app-id:fakeAppID", "POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with InvalidArgument Response - 400 Bad request", func(t *testing.T) {
		d := &epb.ErrorInfo{
			Reason: "fakeReason",
		}
		details, _ := anypb.New(d)

		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(
			int32(codes.InvalidArgument),
			"InvalidArgument",
			[]*anypb.Any{details},
		)
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")
		defer fakeInternalErrorResponse.Close()

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				"fakeAppID",
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeInternalErrorResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 400, resp.StatusCode)

		// protojson produces different indentation space based on OS
		// For linux
		comp1 := string(resp.RawBody) == `{"code":3,"message":"InvalidArgument","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"fakeReason"}]}`
		// For mac and windows
		comp2 := string(resp.RawBody) == `{"code":3, "message":"InvalidArgument", "details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo", "reason":"fakeReason"}]}`
		assert.True(t, comp1 || comp2)
	})

	t.Run("Invoke direct messaging with InvalidArgument Response for external invocation - 400 Bad request", func(t *testing.T) {
		d := &epb.ErrorInfo{
			Reason: "fakeReason",
		}
		details, _ := anypb.New(d)

		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(
			int32(codes.InvalidArgument),
			"InvalidArgument",
			[]*anypb.Any{details},
		)
		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod"
		fakeData := []byte("fakeData")
		defer fakeInternalErrorResponse.Close()

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				"http://api.github.com",
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeInternalErrorResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 400, resp.StatusCode)

		// protojson produces different indentation space based on OS
		// For linux
		comp1 := string(resp.RawBody) == `{"code":3,"message":"InvalidArgument","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"fakeReason"}]}`
		// For mac and windows
		comp2 := string(resp.RawBody) == `{"code":3, "message":"InvalidArgument", "details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo", "reason":"fakeReason"}]}`
		assert.True(t, comp1 || comp2)
	})

	t.Run("Invoke direct messaging with malformed status response", func(t *testing.T) {
		malformedDetails := &anypb.Any{TypeUrl: "malformed"}
		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(int32(codes.Internal), "InternalError", []*anypb.Any{malformedDetails})
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")
		defer fakeInternalErrorResponse.Close()

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				"fakeAppID",
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeInternalErrorResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Truef(t, strings.HasPrefix(string(resp.RawBody), "{\"errorCode\":\"ERR_MALFORMED_RESPONSE\",\"message\":\""), "code not found in response: %v", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with malformed status response for external invocation", func(t *testing.T) {
		malformedDetails := &anypb.Any{TypeUrl: "malformed"}
		fakeInternalErrorResponse := invokev1.NewInvokeMethodResponse(int32(codes.Internal), "InternalError", []*anypb.Any{malformedDetails})
		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod"
		fakeData := []byte("fakeData")
		defer fakeInternalErrorResponse.Close()

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				"http://api.github.com",
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeInternalErrorResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.True(t, strings.HasPrefix(string(resp.RawBody), "{\"errorCode\":\"ERR_MALFORMED_RESPONSE\",\"message\":\""))
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.MatchedBy(func(imr *invokev1.InvokeMethodRequest) bool {
					if imr == nil {
						return false
					}

					msg := imr.Message()
					if msg.GetMethod() != "fakeMethod" {
						return false
					}

					if imr.EncodeHTTPQueryString() != "param1=val1&param2=val2" {
						return false
					}

					return true
				}),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging with querystring for external invocation - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.MatchedBy(func(imr *invokev1.InvokeMethodRequest) bool {
					if imr == nil {
						return false
					}

					msg := imr.Message()
					if msg.GetMethod() != "fakeMethod" {
						return false
					}

					if imr.EncodeHTTPQueryString() != "param1=val1&param2=val2" {
						return false
					}

					return true
				}),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging - HEAD - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("HEAD", apiPath, nil, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody) // Empty body for HEAD
	})

	t.Run("Invoke direct messaging route '/' - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/fakeAppID/method/"

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke direct messaging route '/' for external invocation - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		apiPath := "v1.0/invoke/http://api.github.com/method/"

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(fakeDirectMessageResponse, nil).
			Once()

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "fakeDirectMessageResponse", string(resp.RawBody))
	})

	t.Run("Invoke returns error - 500 ERR_DIRECT_INVOKE", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(nil, errors.New("UPSTREAM_ERROR")).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	t.Run("Invoke returns error - 500 ERR_DIRECT_INVOKE for external invocation", func(t *testing.T) {
		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(nil, errors.New("UPSTREAM_ERROR")).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	t.Run("Invoke returns error - 403 ERR_DIRECT_INVOKE", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "fakeAppID"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(nil, status.Errorf(codes.PermissionDenied, "Permission Denied")).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	t.Run("Invoke returns error - 403 ERR_DIRECT_INVOKE for external invocation", func(t *testing.T) {
		apiPath := "v1.0/invoke/http://api.github.com/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count

		mockDirectMessaging.
			On(
				"Invoke",
				mock.MatchedBy(matchContextInterface),
				mock.MatchedBy(func(b string) bool {
					return b == "http://api.github.com"
				}),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(nil, status.Errorf(codes.PermissionDenied, "Permission Denied")).
			Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_DIRECT_INVOKE", resp.ErrorBody["errorCode"])
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpointsWithTracer(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	var buffer string
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	compStore := compstore.New()
	testAPI := &api{
		directMessaging: mockDirectMessaging,
		tracingSpec:     spec,
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints(), &fakeHTTPServerOptions{
		spec: &spec,
	})

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with dapr-app-id - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		buffer = ""
		apiPath := "fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "dapr-app-id", "fakeAppID")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		buffer = ""
		apiPath := "v1.0/invoke/fakeAppID/method/fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}),
			mock.MatchedBy(func(imr *invokev1.InvokeMethodRequest) bool {
				if imr == nil {
					return false
				}

				msg := imr.Message()
				if msg.GetMethod() != "fakeMethod" {
					return false
				}

				if imr.EncodeHTTPQueryString() != "param1=val1&param2=val2" {
					return false
				}

				return true
			}),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging with querystring and app-id in header - 200 OK", func(t *testing.T) {
		fakeDirectMessageResponse := getFakeDirectMessageResponse()
		defer fakeDirectMessageResponse.Close()

		buffer = ""
		apiPath := "fakeMethod?param1=val1&param2=val2"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeAppID"
			}),
			mock.MatchedBy(func(imr *invokev1.InvokeMethodRequest) bool {
				if imr == nil {
					return false
				}

				msg := imr.Message()
				if msg.GetMethod() != "fakeMethod" {
					return false
				}

				if imr.EncodeHTTPQueryString() != "param1=val1&param2=val2" {
					return false
				}

				return true
			}),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "dapr-app-id", "fakeAppID")

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		assert.Equal(t, 200, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1DirectMessagingEndpointsWithResiliency(t *testing.T) {
	failingDirectMessaging := &daprt.FailingDirectMessaging{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingKey":        1,
				"extraFailingKey":   3,
				"circuitBreakerKey": 10,
			},
			map[string]time.Duration{
				"timeoutKey": time.Second * 30,
			},
			map[string]int{},
		),
	}

	fakeServer := newFakeHTTPServer()
	compStore := compstore.New()
	testAPI := &api{
		directMessaging: failingDirectMessaging,
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.FromConfigurations(logger.NewLogger("messaging.test"), testResiliency),
		}),
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints(), nil)

	t.Run("Test invoke direct message does not retry on 200", func(t *testing.T) {
		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("allgood")

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 1, failingDirectMessaging.Failure.CallCount("allgood"))
		assert.Equal(t, fakeData, resp.RawBody)
	})

	t.Run("Test invoke direct message does not retry on 2xx", func(t *testing.T) {
		failingDirectMessaging.SuccessStatusCode = 201
		defer func() {
			failingDirectMessaging.SuccessStatusCode = 0
		}()

		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("allgood2")

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		assert.Equal(t, 201, resp.StatusCode)
		assert.Equal(t, 1, failingDirectMessaging.Failure.CallCount("allgood2"))
		assert.Equal(t, fakeData, resp.RawBody)
	})

	t.Run("Test invoke direct message retries with resiliency", func(t *testing.T) {
		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("failingKey")

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("failingKey"))
	})

	t.Run("Test invoke direct message retries on unsuccessful statuscode with resiliency", func(t *testing.T) {
		failingDirectMessaging.SuccessStatusCode = 450
		defer func() {
			failingDirectMessaging.SuccessStatusCode = 0
		}()
		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("failingKey2")

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil, "header1", "val1")

		assert.Equal(t, 450, resp.StatusCode)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("failingKey2"))
		assert.Equal(t, string(fakeData), string(resp.RawBody))
		assert.Equal(t, "val1", resp.RawHeader.Get("header1"))
		assert.Equal(t, "application/json", resp.ContentType)
	})

	t.Run("Test invoke direct message fails with timeout", func(t *testing.T) {
		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("timeoutKey")

		// act
		start := time.Now()
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("timeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("Test invoke direct messages fails after exhausting retries", func(t *testing.T) {
		apiPath := "v1.0/invoke/failingApp/method/fakeMethod"
		fakeData := []byte("extraFailingKey")

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("extraFailingKey"))
	})

	t.Run("Test invoke direct messages can trip circuit breaker", func(t *testing.T) {
		apiPath := "v1.0/invoke/circuitBreakerApp/method/fakeMethod"
		fakeData := []byte("circuitBreakerKey")

		// Circuit Breaker trips on the 5th failure, stopping retries.
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount("circuitBreakerKey"))

		// Request occurs when the circuit breaker is open, which shouldn't even hit the app.
		resp = fakeServer.DoRequest("POST", apiPath, fakeData, nil)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Contains(t, string(resp.RawBody), "circuit breaker is open", "Should have received a circuit breaker open error.")
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount("circuitBreakerKey"))
	})

	fakeServer.Shutdown()
}

func TestPathHasPrefix(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		prefixParts  []string
		want         int
		wantTrailing string
	}{
		{name: "match one prefix", path: "/v1.0/invoke/foo", prefixParts: []string{"v1.0"}, want: 6, wantTrailing: "invoke/foo"},
		{name: "match two prefixes", path: "/v1.0/invoke/foo", prefixParts: []string{"v1.0", "invoke"}, want: 13, wantTrailing: "foo"},
		{name: "ignore extra slashes", path: "//v1.0///invoke//foo", prefixParts: []string{"v1.0", "invoke"}, want: 17, wantTrailing: "foo"},
		{name: "extra slashes after match", path: "//v1.0///invoke//foo//bar", prefixParts: []string{"v1.0", "invoke"}, want: 17, wantTrailing: "foo//bar"},
		{name: "no slash at beginning", path: "v1.0//invoke//foo", prefixParts: []string{"v1.0", "invoke"}, want: 14, wantTrailing: "foo"},
		{name: "empty prefix", path: "/foo/bar", prefixParts: []string{}, want: 1, wantTrailing: "foo/bar"},
		{name: "empty path", path: "", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "no match", path: "/foo/bar", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "path is slash only", path: "/", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "empty prefix skips multiple slashes", path: "///", prefixParts: []string{}, want: 3, wantTrailing: ""},
		{name: "missing initial part", path: "/foo/bar", prefixParts: []string{"bar"}, want: -1},
		{name: "match incomplete", path: "/v1.0", prefixParts: []string{"v1.0", "invoke"}, want: -1},
		{name: "trailing slash is required", path: "v1.0//invoke", prefixParts: []string{"v1.0", "invoke"}, want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathHasPrefix(tt.path, tt.prefixParts...)
			if got != tt.want {
				t.Errorf("pathHasPrefix() = %v, want %v", got, tt.want)
			} else if got >= 0 && tt.path[got:] != tt.wantTrailing {
				t.Errorf("trailing = %q, want %q", tt.path[got:], tt.wantTrailing)
			}
		})
	}
}

func TestFindTargetIDAndMethod(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		headers      http.Header
		wantTargetID string
		wantMethod   string
	}{
		{name: "dapr-app-id header", path: "/foo/bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
		{name: "basic auth", path: "/foo/bar", headers: http.Header{"Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "auth", wantMethod: "foo/bar"},
		{name: "dapr-app-id header has priority over basic auth", path: "/foo/bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}, "Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
		{name: "path with internal target", path: "/v1.0/invoke/myapp/method/foo", wantTargetID: "myapp", wantMethod: "foo"},
		{name: "basic auth has priority over path", path: "/v1.0/invoke/myapp/method/foo", headers: http.Header{"Authorization": []string{"Basic ZGFwci1hcHAtaWQ6YXV0aA=="}}, wantTargetID: "auth", wantMethod: "v1.0/invoke/myapp/method/foo"},
		{name: "path with '/' method", path: "/v1.0/invoke/myapp/method/", wantTargetID: "myapp", wantMethod: ""},
		{name: "path with missing method", path: "/v1.0/invoke/myapp/method", wantTargetID: "", wantMethod: ""},
		{name: "path with http target unescaped", path: "/v1.0/invoke/http://example.com/method/foo", wantTargetID: "http://example.com", wantMethod: "foo"},
		{name: "path with https target unescaped", path: "/v1.0/invoke/https://example.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "path with http target escaped", path: "/v1.0/invoke/http%3A%2F%2Fexample.com/method/foo", wantTargetID: "http://example.com", wantMethod: "foo"},
		{name: "path with https target escaped", path: "/v1.0/invoke/https%3A%2F%2Fexample.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "path with https target partly escaped", path: "/v1.0/invoke/https%3A/%2Fexample.com/method/foo", wantTargetID: "https://example.com", wantMethod: "foo"},
		{name: "extra slashes are removed", path: "///foo//bar", headers: http.Header{"Dapr-App-Id": []string{"myapp"}}, wantTargetID: "myapp", wantMethod: "foo/bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTargetID, gotMethod := findTargetIDAndMethod(tt.path, tt.headers)
			if gotTargetID != tt.wantTargetID {
				t.Errorf("findTargetIDAndMethod() gotTargetID = %v, want %v", gotTargetID, tt.wantTargetID)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("findTargetIDAndMethod() gotMethod = %v, want %v", gotMethod, tt.wantMethod)
			}
		})
	}
}

func getFakeDirectMessageResponse() *invokev1.InvokeMethodResponse {
	return getFakeDirectMessageResponseWithStatusCode(http.StatusOK)
}

func getFakeDirectMessageResponseWithStatusCode(code int) *invokev1.InvokeMethodResponse {
	return invokev1.NewInvokeMethodResponse(int32(code), http.StatusText(code), nil).
		WithRawDataString("fakeDirectMessageResponse").
		WithContentType("application/json")
}
