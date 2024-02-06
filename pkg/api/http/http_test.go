/*
Copyright 2021 The Dapr Authors
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	gohttp "net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/test/bufconn"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	workflowContrib "github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/api/universal"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/expr"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/middleware"
	middlewarehttp "github.com/dapr/dapr/pkg/middleware/http"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	daprt "github.com/dapr/dapr/pkg/testing"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const bufconnBufSize = 2 << 20 // 2MB

var invalidJSON = []byte{0x7b, 0x7b}

var testResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  ptr.Of(1),
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
				"tenRetries": {
					MaxRetries:  ptr.Of(10),
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
			},
			Timeouts: map[string]string{
				"fast": "100ms",
			},
			CircuitBreakers: map[string]v1alpha1.CircuitBreaker{
				"simpleCB": {
					MaxRequests: 1,
					Timeout:     "1s",
					Trip:        "consecutiveFailures > 4",
				},
			},
		},
		Targets: v1alpha1.Targets{
			Apps: map[string]v1alpha1.EndpointPolicyNames{
				"failingApp": {
					Retry:   "singleRetry",
					Timeout: "fast",
				},
				"circuitBreakerApp": {
					Retry:          "tenRetries",
					CircuitBreaker: "simpleCB",
				},
			},
			Components: map[string]v1alpha1.ComponentPolicyNames{
				"failSecret": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
				"failStore": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
			},
			Actors: map[string]v1alpha1.ActorPolicyNames{
				"failingActorType": {
					Retry:               "singleRetry",
					Timeout:             "fast",
					CircuitBreakerScope: "type",
				},
			},
		},
	},
}

func TestPubSubEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			CompStore: compstore.New(),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{
			PublishFn: func(ctx context.Context, req *pubsub.PublishRequest) error {
				if req.PubsubName == "errorpubsub" {
					return fmt.Errorf("Error from pubsub %s", req.PubsubName)
				}

				if req.PubsubName == "errnotfound" {
					return runtimePubsub.NotFoundError{PubsubName: "errnotfound"}
				}

				if req.PubsubName == "errnotallowed" {
					return runtimePubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				}

				return nil
			},
		},
	}

	mock := daprt.MockPubSub{}
	mock.On("Features").Return([]pubsub.Feature{})
	testAPI.universal.CompStore().AddPubSub("pubsubname", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errorpubsub", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errnotfound", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errnotallowed", compstore.PubsubItem{Component: &mock})

	fakeServer.StartServer(testAPI.constructPubSubEndpoints(), nil)

	t.Run("Publish successfully - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		}
	})

	t.Run("Publish multi path successfully - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/A/B/C", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		}
	})

	t.Run("Publish unsuccessfully - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errorpubsub/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 500, resp.StatusCode, "expected internal server error as response")
			assert.Equal(t, "ERR_PUBSUB_PUBLISH_MESSAGE", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Publish without topic name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish without topic name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish with topic name '/' - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/%%2F", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "success publishing with %s", method)
		}
	})

	t.Run("Publish without topic or pubsub name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Publish without topic or pubsub name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Pubsub not configured - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/pubsubname/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		savePubSubAdapter := testAPI.pubsubAdapter
		testAPI.pubsubAdapter = nil
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_CONFIGURED", resp.ErrorBody["errorCode"])
		}
		testAPI.pubsubAdapter = savePubSubAdapter
	})

	t.Run("Pubsub not configured - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errnotfound/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_FOUND", resp.ErrorBody["errorCode"])
			assert.Equal(t, "pubsub 'errnotfound' not found", resp.ErrorBody["message"])
		}
	})

	t.Run("Pubsub not configured - 403", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/errnotallowed/topic", apiVersionV1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte(`{"key": "value"}`), nil)
			// assert
			assert.Equal(t, 403, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_FORBIDDEN", resp.ErrorBody["errorCode"])
			assert.Equal(t, "topic topic is not allowed for app id fakeAPI", resp.ErrorBody["message"]) //nolint:dupword
		}
	})

	fakeServer.Shutdown()
}

func TestBulkPubSubEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			CompStore: compstore.New(),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{
			BulkPublishFn: func(ctx context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
				switch req.PubsubName {
				case "errorpubsub":
					err := fmt.Errorf("Error from pubsub %s", req.PubsubName)
					res := pubsub.BulkPublishResponse{}
					for _, entry := range req.Entries {
						_, shouldErr := entry.Metadata["shouldErr"]
						if shouldErr {
							res.FailedEntries = append(res.FailedEntries, pubsub.BulkPublishResponseFailedEntry{
								EntryId: entry.EntryId,
								Error:   err,
							})
						}
					}
					return res, err
				case "errnotfound":
					return pubsub.BulkPublishResponse{}, runtimePubsub.NotFoundError{PubsubName: "errnotfound"}
				case "errnotallowed":
					return pubsub.BulkPublishResponse{}, runtimePubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				default:
					return pubsub.BulkPublishResponse{}, nil
				}
			},
		},
	}

	mock := daprt.MockPubSub{}
	mock.On("Features").Return([]pubsub.Feature{})
	testAPI.universal.CompStore().AddPubSub("pubsubname", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errorpubsub", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errnotfound", compstore.PubsubItem{Component: &mock})
	testAPI.universal.CompStore().AddPubSub("errnotallowed", compstore.PubsubItem{Component: &mock})

	fakeServer.StartServer(testAPI.constructPubSubEndpoints(), nil)

	bulkRequest := []bulkPublishMessageEntry{
		{
			EntryID: "1",
			Event: map[string]string{
				"key":   "first",
				"value": "first value",
			},
			ContentType: "application/json",
		},
		{
			EntryID: "2",
			Event: map[string]string{
				"key":   "second",
				"value": "second value",
			},
			ContentType: "application/json",
			Metadata: map[string]string{
				"md1": "mdVal1",
				"md2": "mdVal2",
			},
		},
		{
			EntryID: "3",
			Event: map[string]string{
				"key":   "third",
				"value": "third value",
			},
			ContentType: "application/json",
			Metadata: map[string]string{
				"cloudevent.source": "unit-test",
				"cloudevent.topic":  "overridetopic",  // noop -- if this modified the envelope the test would fail
				"cloudevent.pubsub": "overridepubsub", // noop -- if this modified the envelope the test would fail
			},
		},
	}

	// setup
	reqBytes, _ := json.Marshal(bulkRequest)
	resBytes := []byte{}
	t.Run("Bulk Publish successfully - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, resBytes, resp.RawBody, "failed to match response on bulk publish")
		}
	})

	t.Run("Bulk Publish multi path successfully - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/A/B/C", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, resBytes, resp.RawBody, "failed to match response on bulk publish")
		}
	})

	t.Run("Bulk Publish complete failure - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/errorpubsub/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}

		errBulkRequest := []bulkPublishMessageEntry{}
		for _, entry := range bulkRequest {
			if entry.Metadata == nil {
				entry.Metadata = map[string]string{}
			}
			entry.Metadata["shouldErr"] = "true"
			errBulkRequest = append(errBulkRequest, entry)
		}

		errBulkResponse := pubsub.BulkPublishResponse{
			FailedEntries: []pubsub.BulkPublishResponseFailedEntry{
				{
					EntryId: "1",
					Error:   errors.New("Error from pubsub errorpubsub"),
				},
				{
					EntryId: "2",
					Error:   errors.New("Error from pubsub errorpubsub"),
				},
				{
					EntryId: "3",
					Error:   errors.New("Error from pubsub errorpubsub"),
				},
			},
		}

		errReqBytes, _ := json.Marshal(errBulkRequest)

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, errReqBytes, nil)
			// assert
			assert.Equal(t, 500, resp.StatusCode, "expected internal server error as response")
			assert.Equal(t, "ERR_PUBSUB_PUBLISH_MESSAGE", resp.ErrorBody["errorCode"])

			bulkResp := BulkPublishResponse{}
			require.NoError(t, json.Unmarshal(resp.RawBody, &bulkResp))
			assert.Equal(t, len(errBulkResponse.FailedEntries), len(bulkResp.FailedEntries))
			for i, entry := range bulkResp.FailedEntries {
				assert.Equal(t, errBulkResponse.FailedEntries[i].EntryId, entry.EntryId)
				assert.Equal(t, errBulkResponse.FailedEntries[i].Error.Error(), entry.Error)
			}
		}
	})

	t.Run("Bulk Publish partial failure - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/errorpubsub/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}

		errBulkRequest := []bulkPublishMessageEntry{}
		for _, entry := range bulkRequest {
			// Fail entries 2 and 3
			if entry.EntryID == "2" || entry.EntryID == "3" {
				if entry.Metadata == nil {
					entry.Metadata = map[string]string{}
				}
				entry.Metadata["shouldErr"] = "true"
			}
			errBulkRequest = append(errBulkRequest, entry)
		}

		errBulkResponse := pubsub.BulkPublishResponse{
			FailedEntries: []pubsub.BulkPublishResponseFailedEntry{
				{
					EntryId: "2",
					Error:   errors.New("Error from pubsub errorpubsub"),
				},
				{
					EntryId: "3",
					Error:   errors.New("Error from pubsub errorpubsub"),
				},
			},
		}

		errReqBytes, _ := json.Marshal(errBulkRequest)

		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, errReqBytes, nil)
			// assert
			assert.Equal(t, 500, resp.StatusCode, "expected internal server error as response")
			assert.Equal(t, "ERR_PUBSUB_PUBLISH_MESSAGE", resp.ErrorBody["errorCode"])

			bulkResp := BulkPublishResponse{}
			require.NoError(t, json.Unmarshal(resp.RawBody, &bulkResp))
			assert.Equal(t, len(errBulkResponse.FailedEntries), len(bulkResp.FailedEntries))
			for i, entry := range bulkResp.FailedEntries {
				assert.Equal(t, errBulkResponse.FailedEntries[i].EntryId, entry.EntryId)
				assert.Equal(t, errBulkResponse.FailedEntries[i].Error.Error(), entry.Error)
			}
		}
	})

	t.Run("Bulk Publish without topic name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Bulk Publish without entryId - 400", func(t *testing.T) {
		reqWithoutEntryId := []bulkPublishMessageEntry{ //nolint:stylecheck
			{
				Event: map[string]string{
					"key":   "first",
					"value": "first value",
				},
				ContentType: "application/json",
			},
			{
				Event: map[string]string{
					"key":   "second",
					"value": "second value",
				},
				ContentType: "application/json",
				Metadata: map[string]string{
					"md1": "mdVal1",
					"md2": "mdVal2",
				},
			},
		}
		reqBytesWithoutEntryId, _ := json.Marshal(reqWithoutEntryId) //nolint:stylecheck
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytesWithoutEntryId, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_EVENTS_SER", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "error: entryId is duplicated or not present for entry")
		}
	})

	t.Run("Bulk Publish with duplicate entryId - 400", func(t *testing.T) {
		reqWithoutEntryId := []bulkPublishMessageEntry{ //nolint:stylecheck
			{
				EntryID: "1",
				Event: map[string]string{
					"key":   "first",
					"value": "first value",
				},
				ContentType: "application/json",
			},
			{
				EntryID: "1",
				Event: map[string]string{
					"key":   "second",
					"value": "second value",
				},
				ContentType: "application/json",
				Metadata: map[string]string{
					"md1": "mdVal1",
					"md2": "mdVal2",
				},
			},
		}
		reqBytesWithoutEntryId, _ := json.Marshal(reqWithoutEntryId) //nolint:stylecheck
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytesWithoutEntryId, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_EVENTS_SER", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "error: entryId is duplicated or not present for entry")
		}
	})

	t.Run("Bulk Publish metadata error - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic?metadata.rawPayload=100", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "failed to publish with %s", method)
			assert.Equal(t, "ERR_PUBSUB_REQUEST_METADATA", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "failed deserializing metadata")
		}
	})

	t.Run("Bulk Publish invalid cloudevent - 400", func(t *testing.T) {
		reqInvalidCE := []bulkPublishMessageEntry{
			{
				EntryID: "1",
				Event: map[string]string{
					"key":   "first",
					"value": "first value",
				},
				ContentType: "application/json",
			},
			{
				EntryID:     "2",
				Event:       "this is not a cloudevent!",
				ContentType: "application/cloudevents+json",
				Metadata: map[string]string{
					"md1": "mdVal1",
					"md2": "mdVal2",
				},
			},
		}
		rBytes, _ := json.Marshal(reqInvalidCE)
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, rBytes, nil)
			// assert
			assert.Equal(t, 500, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_CLOUD_EVENTS_SER", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "cannot create cloudevent")
		}
	})

	t.Run("Bulk Publish dataContentType mismatch - 400", func(t *testing.T) {
		dCTMismatch := []bulkPublishMessageEntry{
			{
				EntryID: "1",
				Event: map[string]string{
					"key":   "first",
					"value": "first value",
				},
				ContentType: "application/json",
			},
			{
				EntryID: "2",
				Event: map[string]string{
					"key":   "second",
					"value": "second value",
				},
				ContentType: "text/xml",
				Metadata: map[string]string{
					"md1": "mdVal1",
					"md2": "mdVal2",
				},
			},
		}
		rBytes, _ := json.Marshal(dCTMismatch)
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, rBytes, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_EVENTS_SER", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "error: mismatch between contentType and event")
		}
	})

	t.Run("Bulk Publish bad request - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, []byte("{\"key\":\"value\"}"), nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_EVENTS_SER", resp.ErrorBody["errorCode"])
			assert.Contains(t, resp.ErrorBody["message"], "error when unmarshaling the request for topic topic") //nolint:dupword
		}
	})

	t.Run("Bulk Publish without topic name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success publishing with %s", method)
		}
	})

	t.Run("Bulk Publish with topic name '/' - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/%%2F", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "success publishing with %s", method)
			assert.Equal(t, resBytes, resp.RawBody, "failed to match response on bulk publish")
		}
	})

	t.Run("Bulk Publish without topic or pubsub name - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success bulk publishing with %s", method)
		}
	})

	t.Run("Bulk Publish without topic or pubsub name ending in / - 404", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 404, resp.StatusCode, "unexpected success bulk publishing with %s", method)
		}
	})

	t.Run("Bulk Publish Pubsub not configured - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/pubsubname/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		// setup
		savePubSubAdapter := testAPI.pubsubAdapter
		testAPI.pubsubAdapter = nil
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success bulk publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_CONFIGURED", resp.ErrorBody["errorCode"])
		}
		testAPI.pubsubAdapter = savePubSubAdapter
	})

	t.Run("Bulk publish Pubsub not found - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/errnotfound/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode, "unexpected success bulk publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_NOT_FOUND", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Bulk publish Pubsub not allowed - 403", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/publish/bulk/errnotallowed/topic", apiVersionV1alpha1)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, reqBytes, nil)
			// assert
			assert.Equal(t, 403, resp.StatusCode, "unexpected success bulk publishing with %s", method)
			assert.Equal(t, "ERR_PUBSUB_FORBIDDEN", resp.ErrorBody["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestShutdownEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	shutdownCh := make(chan struct{})
	testAPI := &api{
		universal: universal.New(universal.Options{
			ShutdownFn: func() {
				close(shutdownCh)
			},
		}),
	}

	fakeServer.StartServer(testAPI.constructShutdownEndpoints(), nil)
	defer fakeServer.Shutdown()

	t.Run("Shutdown successfully - 204", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/shutdown", apiVersionV1)
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 204, resp.StatusCode)
		select {
		case <-time.After(time.Second):
			t.Fatal("Did not shut down within 1 second")
		case <-shutdownCh:
			// All good
		}
	})
}

func TestGetStatusCodeFromMetadata(t *testing.T) {
	t.Run("status code present", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{
			http.HTTPStatusCode: "404",
		})
		assert.Equal(t, 404, res, "expected status code to match")
	})
	t.Run("status code not present", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{})
		assert.Equal(t, 200, res, "expected status code to match")
	})
	t.Run("status code present but invalid", func(t *testing.T) {
		res := GetStatusCodeFromMetadata(map[string]string{
			http.HTTPStatusCode: "a12a",
		})
		assert.Equal(t, 200, res, "expected status code to match")
	})
}

func TestGetMetadataFromRequest(t *testing.T) {
	// set
	r, _ := gohttp.NewRequest(gohttp.MethodGet, "http://test.example.com/resource?metadata.test=test&&other=other", nil)

	// act
	m := getMetadataFromRequest(r)

	// assert
	assert.NotEmpty(t, m, "expected map to be populated")
	assert.Len(t, m, 1, "expected length to match")
	assert.Equal(t, "test", m["test"], "test", "expected value to be equal")
}

func TestGetMetadataFromFastHTTPRequest(t *testing.T) {
	t.Run("request with query args", func(t *testing.T) {
		// set
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.SetRequestURI("http://test.example.com/resource?metadata.test=test&&other=other")

		// act
		m := getMetadataFromFastHTTPRequest(ctx)

		// assert
		assert.NotEmpty(t, m, "expected map to be populated")
		assert.Len(t, m, 1, "expected length to match")
		assert.Equal(t, "test", m["test"], "test", "expected value to be equal")
	})
}

func TestV1OutputBindingsEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	testAPI := &api{
		sendToOutputBindingFn: func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			if name == "testbinding" {
				return nil, nil
			}
			return &bindings.InvokeResponse{Data: []byte("testresponse")}, nil
		},
	}
	fakeServer.StartServer(testAPI.constructBindingsEndpoints(), nil)

	t.Run("Invoke output bindings - 204 No Content empty response", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to invoke output binding with %s", method)
			assert.Equal(t, []byte{}, resp.RawBody, "expected response to match")
		}
	})

	t.Run("Invoke output bindings - 200 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testresponse", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 200, resp.StatusCode, "failed to invoke output binding with %s", method)
			assert.Equal(t, []byte("testresponse"), resp.RawBody, "expected response to match")
		}
	})

	t.Run("Invoke output bindings - 400 InternalError invalid req", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testresponse", apiVersionV1)
		req := `{"dat" : "invalid request"}`
		b, _ := json.Marshal(&req)
		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)
			// assert
			assert.Equal(t, 400, resp.StatusCode)
			assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"])
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testAPI.sendToOutputBindingFn = func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			return nil, errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestV1OutputBindingsEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	testAPI := &api{
		sendToOutputBindingFn: func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			return nil, nil
		},
		tracingSpec: spec,
	}
	fakeServer.StartServer(testAPI.constructBindingsEndpoints(), &fakeHTTPServerOptions{
		spec: &spec,
	})

	t.Run("Invoke output bindings - 204 OK", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/testbinding", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 204, resp.StatusCode, "failed to invoke output binding with %s", method)
		}
	})

	t.Run("Invoke output bindings - 500 InternalError", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/bindings/notfound", apiVersionV1)
		req := OutputBindingRequest{
			Data: "fake output",
		}
		b, _ := json.Marshal(&req)

		testAPI.sendToOutputBindingFn = func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			return nil, errors.New("missing binding name")
		}

		testMethods := []string{"POST", "PUT"}
		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, b, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode)
			assert.Equal(t, "ERR_INVOKE_OUTPUT_BINDING", resp.ErrorBody["errorCode"])
		}
	})

	fakeServer.Shutdown()
}

func TestV1ActorEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	rc := resiliency.FromConfigurations(logger.NewLogger("test.api.http.actors"), testResiliency)
	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Resiliency: rc,
		}),
	}
	testAPI.universal.SetActorsInitDone()

	fakeServer.StartServer(testAPI.constructActorEndpoints(), nil)

	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	fakeData, _ := json.Marshal(fakeBodyObject)

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		apisAndMethods := map[string][]string{
			"v1.0/actors/fakeActorType/fakeActorID/state/key1":          {"GET"},
			"v1.0/actors/fakeActorType/fakeActorID/state":               {"POST", "PUT"},
			"v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1": {"POST", "PUT", "GET", "DELETE"},
			"v1.0/actors/fakeActorType/fakeActorID/method/method1":      {"POST", "PUT", "GET", "DELETE"},
			"v1.0/actors/fakeActorType/fakeActorID/timers/timer1":       {"POST", "PUT", "DELETE"},
		}
		testAPI.universal.SetActorRuntime(nil)

		for apiPath, testMethods := range apisAndMethods {
			for _, method := range testMethods {
				t.Run(fmt.Sprintf("%s %s", method, apiPath), func(t *testing.T) {
					// act
					resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

					// assert
					assert.Equal(t, 500, resp.StatusCode, apiPath)
					assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"])
				})
			}
		}
	})

	t.Run("All PUT/POST APIs - 400 for invalid JSON", func(t *testing.T) {
		testAPI.universal.SetActorRuntime(new(actors.MockActors))
		apiPaths := []string{
			"v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1",
			"v1.0/actors/fakeActorType/fakeActorID/state",
			"v1.0/actors/fakeActorType/fakeActorID/timers/timer1",
		}

		for _, apiPath := range apiPaths {
			for _, requestMethod := range []string{"PUT", "POST"} {
				// {{
				inputBodyBytes := invalidJSON

				// act
				resp := fakeServer.DoRequest(requestMethod, apiPath, inputBodyBytes, nil)

				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"])
			}
		}
	})

	t.Run("Get actor state - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
			Metadata: map[string]string{
				"ttlExpireTime": "2020-01-01T00:00:00Z",
			},
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, fakeData, resp.RawBody)
		assert.Equal(t, "2020-01-01T00:00:00Z", resp.RawHeader.Get("metadata.ttlexpiretime"))
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody)
		assert.Empty(t, resp.RawHeader["Metadata.ttlexpiretime"])
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 500 on GetState failure", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(nil, errors.New("UPSTREAM_ERROR"))

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_STATE_GET", resp.ErrorBody["errorCode"])
		assert.Empty(t, resp.RawHeader["Metadata.ttlexpiretime"])
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Get actor state - 400 for missing actor instace", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(func(*actors.ActorHostedRequest) bool { return false })

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Empty(t, resp.RawHeader["Metadata.ttlexpiretime"])
		assert.Equal(t, "ERR_ACTOR_INSTANCE_MISSING", resp.ErrorBody["errorCode"])
	})

	t.Run("Transaction - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actors.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
	})

	t.Run("Transaction - 400 when actor instance not present", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actors.MockActors)
		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(func(*actors.ActorHostedRequest) bool { return false })

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Empty(t, resp.RawHeader["Metadata.ttlexpiretime"])
		assert.Equal(t, "ERR_ACTOR_INSTANCE_MISSING", resp.ErrorBody["errorCode"])
	})

	t.Run("Transaction - 500 when transactional state operation fails", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actors.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(errors.New("UPSTREAM_ERROR"))

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
		mockActors.AssertNumberOfCalls(t, "IsActorHosted", 1)
		assert.Empty(t, resp.RawHeader["Metadata.ttlexpiretime"])
		assert.Equal(t, "ERR_ACTOR_STATE_TRANSACTION_SAVE", resp.ErrorBody["errorCode"])
	})

	t.Run("Reminder Create - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		reminderRequest := actors.CreateReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      json.RawMessage("null"),
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(actors.MockActors)

		mockActors.On("CreateReminder", &reminderRequest).Return(nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(reminderRequest)

		require.NoError(t, err)
		for _, method := range []string{"POST", "PUT"} {
			resp := fakeServer.DoRequest(method, apiPath, inputBodyBytes, nil)
			assert.Equal(t, 204, resp.StatusCode)
		}

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateReminder", 2)
	})

	t.Run("Reminder Create - 500 when CreateReminderFails", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		reminderRequest := actors.CreateReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      json.RawMessage("null"),
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(actors.MockActors)

		mockActors.On("CreateReminder", &reminderRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(reminderRequest)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_CREATE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "CreateReminder", 1)
	})

	t.Run("Reminder Create - 403 when actor type is not hosted", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		mockActors := new(actors.MockActors)
		mockActors.
			On("CreateReminder", mock.AnythingOfType("*internal.CreateReminderRequest")).
			Return(actors.ErrReminderOpActorNotHosted)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("POST", apiPath, []byte("{}"), nil)

		// assert
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_NON_HOSTED", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "CreateReminder", 1)
	})

	t.Run("Reminder Delete - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.DeleteReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("DeleteReminder", &reminderRequest).Return(nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "DeleteReminder", 1)
	})

	t.Run("Reminder Delete - 500 on upstream actor error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.DeleteReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("DeleteReminder", &reminderRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_DELETE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "DeleteReminder", 1)
	})

	t.Run("Reminder Delete - 403 when actor type is not hosted", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		mockActors := new(actors.MockActors)
		mockActors.
			On("DeleteReminder", mock.AnythingOfType("*internal.DeleteReminderRequest")).
			Return(actors.ErrReminderOpActorNotHosted)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, []byte("{}"), nil)

		// assert
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_NON_HOSTED", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "DeleteReminder", 1)
	})

	t.Run("Reminder Get - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(nil, nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Reminder Get - 500 on upstream actor error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(nil, errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_GET", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Reminder Get - 403 when actor type is not hosted", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"

		mockActors := new(actors.MockActors)
		mockActors.
			On("GetReminder", mock.AnythingOfType("*internal.GetReminderRequest")).
			Return(nil, actors.ErrReminderOpActorNotHosted)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 403, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_NON_HOSTED", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Reminder Get - 500 on JSON encode failure from actor", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/reminders/reminder1"
		reminderRequest := actors.GetReminderRequest{
			Name:      "reminder1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		reminderResponse := actors.MockReminder{
			// This is not valid JSON
			Data: json.RawMessage(`foo`),
		}

		mockActors := new(actors.MockActors)

		mockActors.On("GetReminder", &reminderRequest).Return(&reminderResponse, nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_REMINDER_GET", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "GetReminder", 1)
	})

	t.Run("Timer Create - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"

		timerRequest := actors.CreateTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      json.RawMessage("null"),
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
			Callback:  "",
		}
		mockActors := new(actors.MockActors)

		mockActors.On("CreateTimer", &timerRequest).Return(nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(timerRequest)

		require.NoError(t, err)
		for _, method := range []string{"POST", "PUT"} {
			resp := fakeServer.DoRequest(method, apiPath, inputBodyBytes, nil)
			assert.Equal(t, 204, resp.StatusCode)
		}

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateTimer", 2)
	})

	t.Run("Timer Create - 500 on upstream error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"

		timerRequest := actors.CreateTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
			Data:      json.RawMessage("null"),
			DueTime:   "0h0m3s0ms",
			Period:    "0h0m7s0ms",
		}
		mockActors := new(actors.MockActors)

		mockActors.On("CreateTimer", &timerRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(timerRequest)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_TIMER_CREATE", resp.ErrorBody["errorCode"])

		// assert
		mockActors.AssertNumberOfCalls(t, "CreateTimer", 1)
	})

	t.Run("Timer Delete - 204 No Conent", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"
		timerRequest := actors.DeleteTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("DeleteTimer", &timerRequest).Return(nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "DeleteTimer", 1)
	})

	t.Run("Timer Delete - 500 For upstream error", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/timers/timer1"
		timerRequest := actors.DeleteTimerRequest{
			Name:      "timer1",
			ActorType: "fakeActorType",
			ActorID:   "fakeActorID",
		}

		mockActors := new(actors.MockActors)

		mockActors.On("DeleteTimer", &timerRequest).Return(errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_TIMER_DELETE", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "DeleteTimer", 1)
	})

	t.Run("Direct Message - Forwards downstream status", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/method/method1"
		mockActors := new(actors.MockActors)
		fakeData := []byte("fakeData")

		response := &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
				Code:    206,
				Message: "OK",
			},
		}
		mockActors.On("Call", mock.MatchedBy(func(m *internalsv1pb.InternalInvokeRequest) bool {
			if m.GetActor().GetActorType() != "fakeActorType" || m.GetActor().GetActorId() != "fakeActorID" {
				return false
			}

			v := m.GetMessage().GetData().GetValue()
			if len(v) == 0 || !bytes.Equal(v, fakeData) {
				return false
			}
			return true
		})).Return(response, nil)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		assert.Equal(t, 206, resp.StatusCode)
		mockActors.AssertNumberOfCalls(t, "Call", 1)
	})

	t.Run("Direct Message - 500 for actor call failure", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/method/method1"
		mockActors := new(actors.MockActors)
		mockActors.On("Call", mock.MatchedBy(func(m *internalsv1pb.InternalInvokeRequest) bool {
			if m.GetActor().GetActorType() != "fakeActorType" || m.GetActor().GetActorId() != "fakeActorID" {
				return false
			}

			v := m.GetMessage().GetData().GetValue()
			if len(v) == 0 || !bytes.Equal(v, []byte("fakeData")) {
				return false
			}
			return true
		})).Return(nil, errors.New("UPSTREAM_ERROR"))

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("POST", apiPath, []byte("fakeData"), nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_ACTOR_INVOKE_METHOD", resp.ErrorBody["errorCode"])
		mockActors.AssertNumberOfCalls(t, "Call", 1)
	})

	failingActors := &actors.FailingActors{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingId": 1,
			},
			map[string]time.Duration{
				"timeoutId": time.Second * 30,
			},
			map[string]int{},
		),
	}

	t.Run("Direct Message - retries with resiliency", func(t *testing.T) {
		testAPI.universal.SetActorRuntime(failingActors)

		msg := []byte("M'illumino d'immenso.")
		apiPath := fmt.Sprintf("v1.0/actors/failingActorType/%s/method/method1", "failingId")
		resp := fakeServer.DoRequest("POST", apiPath, msg, nil)

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, msg, resp.RawBody)
		assert.Equal(t, 2, failingActors.Failure.CallCount("failingId"))
	})

	fakeServer.Shutdown()
}

func TestV1MetadataEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	compStore := compstore.New()
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsV1alpha1.Component{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockComponent1Name",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "mock.component1Type",
			Version: "v1.0",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "actorMockComponent1",
					Value: commonapi.DynamicValue{
						JSON: apiextensionsV1.JSON{Raw: []byte("true")},
					},
				},
			},
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsV1alpha1.Component{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockComponent2Name",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "mock.component2Type",
			Version: "v1.0",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "actorMockComponent2",
					Value: commonapi.DynamicValue{
						JSON: apiextensionsV1.JSON{Raw: []byte("true")},
					},
				},
			},
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())
	compStore.SetSubscriptions([]runtimePubsub.Subscription{
		{
			PubsubName:      "test",
			Topic:           "topic",
			DeadLetterTopic: "dead",
			Metadata:        map[string]string{},
			Rules: []*runtimePubsub.Rule{
				{
					Match: &expr.Expr{},
					Path:  "path",
				},
			},
		},
	})
	compStore.AddHTTPEndpoint(httpEndpointsV1alpha1.HTTPEndpoint{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockHTTPEndpoint",
		},
		Spec: httpEndpointsV1alpha1.HTTPEndpointSpec{
			BaseURL: "api.test.com",
			Headers: []commonapi.NameValuePair{
				{
					Name: "Accept-Language",
					Value: commonapi.DynamicValue{
						JSON: apiextensionsV1.JSON{Raw: []byte("en-US")},
					},
				},
			},
		},
	})

	mockActors := new(actors.MockActors)
	mockActors.On("GetRuntimeStatus")

	appConnectionConfig := config.AppConnectionConfig{
		ChannelAddress:      "1.2.3.4",
		MaxConcurrency:      10,
		Port:                5000,
		Protocol:            "http",
		HealthCheckHTTPPath: "/healthz",
		HealthCheck: &config.AppHealthConfig{
			ProbeInterval: 10 * time.Second,
			ProbeTimeout:  5 * time.Second,
			ProbeOnly:     true,
			Threshold:     3,
		},
	}

	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID:     "xyz",
			CompStore: compStore,
			GetComponentsCapabilitiesFn: func() map[string][]string {
				capsMap := make(map[string][]string)
				capsMap["MockComponent1Name"] = []string{"mock.feat.MockComponent1Name"}
				capsMap["MockComponent2Name"] = []string{"mock.feat.MockComponent2Name"}
				return capsMap
			},
			ExtendedMetadata: map[string]string{
				"test": "value",
			},
			AppConnectionConfig: appConnectionConfig,
			GlobalConfig:        &config.Configuration{},
		}),
	}
	testAPI.universal.SetActorRuntime(mockActors)
	testAPI.universal.SetActorsInitDone()

	fakeServer.StartServer(testAPI.constructMetadataEndpoints(), nil)

	t.Run("Set Metadata", func(t *testing.T) {
		resp := fakeServer.DoRequest("PUT", "v1.0/metadata/foo", []byte("bar"), nil)
		assert.Equal(t, 204, resp.StatusCode)
	})

	const expectedBody = `{"id":"xyz","runtimeVersion":"edge","actors":[{"type":"abcd","count":10},{"type":"xyz","count":5}],"components":[{"name":"MockComponent1Name","type":"mock.component1Type","version":"v1.0","capabilities":["mock.feat.MockComponent1Name"]},{"name":"MockComponent2Name","type":"mock.component2Type","version":"v1.0","capabilities":["mock.feat.MockComponent2Name"]}],"extended":{"daprRuntimeVersion":"edge","foo":"bar","test":"value"},"subscriptions":[{"pubsubname":"test","topic":"topic","rules":[{"path":"path"}],"deadLetterTopic":"dead"}],"httpEndpoints":[{"name":"MockHTTPEndpoint"}],"appConnectionProperties":{"port":5000,"protocol":"http","channelAddress":"1.2.3.4","maxConcurrency":10,"health":{"healthCheckPath":"/healthz","healthProbeInterval":"10s","healthProbeTimeout":"5s","healthThreshold":3}},"actorRuntime":{"runtimeStatus":"RUNNING","activeActors":[{"type":"abcd","count":10},{"type":"xyz","count":5}],"hostReady":true}}`

	t.Run("Get Metadata", func(t *testing.T) {
		resp := fakeServer.DoRequest("GET", "v1.0/metadata", nil, nil)

		// Compact the response JSON to harmonize it
		if len(resp.RawBody) > 0 {
			compact := &bytes.Buffer{}
			err := json.Compact(compact, resp.RawBody)
			require.NoError(t, err)
			resp.RawBody = compact.Bytes()
		}

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, expectedBody, string(resp.RawBody))
		mockActors.AssertNumberOfCalls(t, "GetRuntimeStatus", 1)
	})

	fakeServer.Shutdown()
}

func createExporters(buffer *string) {
	exporter := testtrace.NewStringExporter(buffer, logger.NewLogger("fakeLogger"))
	exporter.Register("fakeID")
}

func TestV1ActorEndpointsWithTracer(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	createExporters(&buffer)

	testAPI := &api{
		universal: universal.New(universal.Options{
			Resiliency: resiliency.New(nil),
		}),
		tracingSpec: spec,
	}
	testAPI.universal.SetActorsInitDone()

	fakeServer.StartServer(testAPI.constructActorEndpoints(), &fakeHTTPServerOptions{
		spec: &spec,
	})

	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	fakeData, _ := json.Marshal(fakeBodyObject)

	t.Run("Actor runtime is not initialized", func(t *testing.T) {
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		testAPI.universal.SetActorRuntime(nil)

		testMethods := []string{"GET"}

		for _, method := range testMethods {
			buffer = ""
			// act
			resp := fakeServer.DoRequest(method, apiPath, fakeData, nil)

			// assert
			assert.Equal(t, 500, resp.StatusCode, apiPath)
			assert.Equal(t, "ERR_ACTOR_RUNTIME_NOT_FOUND", resp.ErrorBody["errorCode"], apiPath)
		}
	})

	t.Run("Get actor state - 200 OK", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state/key1"
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: fakeData,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, fakeData, resp.RawBody)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})

	t.Run("Transaction - 204 No Content", func(t *testing.T) {
		buffer = ""
		apiPath := "v1.0/actors/fakeActorType/fakeActorID/state"

		testTransactionalOperations := []actors.TransactionalOperation{
			{
				Operation: actors.Upsert,
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: actors.Delete,
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		mockActors := new(actors.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:    "fakeActorID",
			ActorType:  "fakeActorType",
			Operations: testTransactionalOperations,
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		testAPI.universal.SetActorRuntime(mockActors)

		// act
		inputBodyBytes, err := json.Marshal(testTransactionalOperations)

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})

	fakeServer.Shutdown()
}

func TestAPIToken(t *testing.T) {
	const token = "1234"

	t.Setenv("DAPR_API_TOKEN", token)

	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataString("fakeDirectMessageResponse").
		WithContentType("application/json")
	defer fakeDirectMessageResponse.Close()

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()
	compStore := compstore.New()

	testAPI := &api{
		directMessaging: mockDirectMessaging,
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints(), &fakeHTTPServerOptions{
		apiAuth: true,
	})

	t.Run("Invoke direct messaging with token - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, token, fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Invoke direct messaging empty token - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, "", fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})

	t.Run("Invoke direct messaging token mismatch - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequestWithAPIToken("POST", apiPath, "4567", fakeData)
		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})

	t.Run("Invoke direct messaging without token - 401", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
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
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 401, resp.StatusCode)
	})
}

func TestEmptyPipelineWithTracer(t *testing.T) {
	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataString("fakeDirectMessageResponse").
		WithContentType("application/json")
	defer fakeDirectMessageResponse.Close()

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1.0"}
	pipe := func(next gohttp.Handler) gohttp.Handler { return next }

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
		spec:     &spec,
		pipeline: pipe,
	})

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
		apiPath := "v1.0/invoke/fakeDaprID/method/fakeMethod"
		fakeData := []byte("fakeData")

		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On(
			"Invoke",
			mock.MatchedBy(matchContextInterface),
			mock.MatchedBy(func(b string) bool {
				return b == "fakeDaprID"
			}),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(fakeDirectMessageResponse, nil).Once()

		// act
		resp := fakeServer.DoRequest("POST", apiPath, fakeData, nil)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		// TODO Check back as how to assert on generated span ID
		// assert.NotEmpty(t, resp.JSONBody, "failed to generate trace context with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})
}

func TestConfigurationGet(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	var fakeConfigurationStore configuration.Store = &fakeConfigurationStore{}

	storeName := "store1"
	badStoreName := "nonExistStore"

	compStore := compstore.New()
	compStore.AddConfiguration(storeName, fakeConfigurationStore)
	testAPI := &api{
		universal: universal.New(universal.Options{
			Resiliency: resiliency.New(nil),
			CompStore:  compStore,
		}),
	}
	fakeServer.StartServer(testAPI.constructConfigurationEndpoints(), nil)

	t.Run("Get configurations with a good key - alpha1", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/configuration/%s?key=%s", storeName, "good-key1")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with good key should return 204")

		// assert
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 1)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Contains(t, rspMap, "good-key1")
		goodkeyVal := rspMap["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal["value"].(string))
		assert.Equal(t, "version1", goodkeyVal["version"].(string))
		metadata := goodkeyVal["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata["metadata-key1"])
	})

	t.Run("Get configurations with a good key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/configuration/%s?key=%s", storeName, "good-key1")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with good key should return 204")

		// assert
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 1)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Contains(t, rspMap, "good-key1")
		goodkeyVal := rspMap["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal["value"].(string))
		assert.Equal(t, "version1", goodkeyVal["version"].(string))
		metadata := goodkeyVal["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata["metadata-key1"])
	})

	t.Run("Get Configurations with good keys-alpha1", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/configuration/%s?key=%s&key=%s", storeName, "good-key1", "good-key2")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with good keys should return 200")
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 2)
		rspMap1 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap1)
		assert.Contains(t, rspMap1, "good-key1")
		goodkeyVal1 := rspMap1["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal1["value"].(string))
		assert.Equal(t, "version1", goodkeyVal1["version"].(string))
		metadata1 := goodkeyVal1["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata1["metadata-key1"])

		rspMap2 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap2)
		assert.Contains(t, rspMap2, "good-key2")
		goodkeyVal2 := rspMap2["good-key2"].(map[string]interface{})
		assert.Equal(t, "good-value2", goodkeyVal2["value"].(string))
		assert.Equal(t, "version2", goodkeyVal2["version"].(string))
		metadata2 := goodkeyVal2["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value2", metadata2["metadata-key2"])
	})

	t.Run("Get Configurations with good keys", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/configuration/%s?key=%s&key=%s", storeName, "good-key1", "good-key2")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with good keys should return 200")
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 2)
		rspMap1 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap1)
		assert.Contains(t, rspMap1, "good-key1")
		goodkeyVal1 := rspMap1["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal1["value"].(string))
		assert.Equal(t, "version1", goodkeyVal1["version"].(string))
		metadata1 := goodkeyVal1["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata1["metadata-key1"])

		rspMap2 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap2)
		assert.Contains(t, rspMap2, "good-key2")
		goodkeyVal2 := rspMap2["good-key2"].(map[string]interface{})
		assert.Equal(t, "good-value2", goodkeyVal2["value"].(string))
		assert.Equal(t, "version2", goodkeyVal2["version"].(string))
		metadata2 := goodkeyVal2["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value2", metadata2["metadata-key2"])
	})

	t.Run("Get All Configurations with empty key - alpha1", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/configuration/%s", storeName)
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with empty key should return 200")

		// assert
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 2)
		rspMap1 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap1)
		assert.Contains(t, rspMap1, "good-key1")
		goodkeyVal1 := rspMap1["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal1["value"].(string))
		assert.Equal(t, "version1", goodkeyVal1["version"].(string))
		metadata1 := goodkeyVal1["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata1["metadata-key1"])

		rspMap2 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap2)
		assert.Contains(t, rspMap2, "good-key2")
		goodkeyVal2 := rspMap2["good-key2"].(map[string]interface{})
		assert.Equal(t, "good-value2", goodkeyVal2["value"].(string))
		assert.Equal(t, "version2", goodkeyVal2["version"].(string))
		metadata2 := goodkeyVal2["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value2", metadata2["metadata-key2"])
	})

	t.Run("Get All Configurations with empty key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/configuration/%s", storeName)
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Accessing configuration store with empty key should return 200")

		// assert
		assert.NotNil(t, resp.JSONBody)
		assert.Len(t, resp.JSONBody.(map[string]interface{}), 2)
		rspMap1 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap1)
		assert.Contains(t, rspMap1, "good-key1")
		goodkeyVal1 := rspMap1["good-key1"].(map[string]interface{})
		assert.Equal(t, "good-value1", goodkeyVal1["value"].(string))
		assert.Equal(t, "version1", goodkeyVal1["version"].(string))
		metadata1 := goodkeyVal1["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value1", metadata1["metadata-key1"])

		rspMap2 := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap2)
		assert.Contains(t, rspMap2, "good-key2")
		goodkeyVal2 := rspMap2["good-key2"].(map[string]interface{})
		assert.Equal(t, "good-value2", goodkeyVal2["value"].(string))
		assert.Equal(t, "version2", goodkeyVal2["version"].(string))
		metadata2 := goodkeyVal2["metadata"].(map[string]interface{})
		assert.Equal(t, "metadata-value2", metadata2["metadata-key2"])
	})

	t.Run("Get Configurations with bad key - alpha1", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/configuration/%s?key=%s", storeName, "bad-key")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "Accessing configuration store with bad key should return 500")
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CONFIGURATION_GET", resp.ErrorBody["errorCode"])
		assert.Equal(t, "failed to get [bad-key] from Configuration store store1: get key error: bad-key", resp.ErrorBody["message"])
	})

	t.Run("Get Configurations with bad key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/configuration/%s?key=%s", storeName, "bad-key")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "Accessing configuration store with bad key should return 500")
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CONFIGURATION_GET", resp.ErrorBody["errorCode"])
		assert.Equal(t, "failed to get [bad-key] from Configuration store store1: get key error: bad-key", resp.ErrorBody["message"])
	})

	t.Run("Get with none exist configurations store - alpha1", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/configuration/%s?key=%s", badStoreName, "good-key1")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode, "Accessing configuration store with none exist configurations store should return 400")
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CONFIGURATION_STORE_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, "configuration store nonExistStore not found", resp.ErrorBody["message"])
	})

	t.Run("Get with none exist configurations store", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/configuration/%s?key=%s", badStoreName, "good-key1")
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode, "Accessing configuration store with none exist configurations store should return 400")
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CONFIGURATION_STORE_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, "configuration store nonExistStore not found", resp.ErrorBody["message"])
	})
}

func TestV1Alpha1ConfigurationUnsubscribe(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	var fakeConfigurationStore configuration.Store = &fakeConfigurationStore{}

	storeName := "store1"

	compStore := compstore.New()
	compStore.AddConfiguration(storeName, fakeConfigurationStore)
	testAPI := &api{
		universal: universal.New(universal.Options{
			Resiliency: resiliency.New(nil),
			CompStore:  compStore,
		}),
		channels: new(channels.Channels),
	}
	fakeServer.StartServer(testAPI.constructConfigurationEndpoints(), nil)

	t.Run("subscribe and unsubscribe configurations - alpha1", func(t *testing.T) {
		apiPath1 := fmt.Sprintf("v1.0-alpha1/configuration/%s/subscribe", storeName)
		resp1 := fakeServer.DoRequest("GET", apiPath1, nil, nil)
		assert.Equal(t, 500, resp1.StatusCode, "subscribe configuration store, should return 500 when app channel is empty")

		rspMap1 := resp1.JSONBody
		assert.Nil(t, rspMap1)

		uuid, err := uuid.NewRandom()
		require.NoError(t, err, "unable to generate id")
		apiPath2 := fmt.Sprintf("v1.0-alpha1/configuration/%s/%s/unsubscribe", storeName, &uuid)

		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)
		assert.Equal(t, 200, resp2.StatusCode, "unsubscribe configuration store,should return 200")
		assert.NotNil(t, resp2.JSONBody, "Unsubscribe configuration should return a non nil response body")
	})

	t.Run("subscribe and unsubscribe configurations", func(t *testing.T) {
		apiPath1 := fmt.Sprintf("v1.0/configuration/%s/subscribe", storeName)
		resp1 := fakeServer.DoRequest("GET", apiPath1, nil, nil)
		assert.Equal(t, 500, resp1.StatusCode, "subscribe configuration store, should return 500 when app channel is empty")

		rspMap1 := resp1.JSONBody
		assert.Nil(t, rspMap1)

		uuid, err := uuid.NewRandom()
		require.NoError(t, err, "unable to generate id")
		apiPath2 := fmt.Sprintf("v1.0/configuration/%s/%s/unsubscribe", storeName, &uuid)

		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)
		assert.Equal(t, 200, resp2.StatusCode, "unsubscribe configuration store,should return 200")
		assert.NotNil(t, resp2.JSONBody, "Unsubscribe configuration should return a non nil response body")
	})

	t.Run("error in unsubscribe configurations - alpha1", func(t *testing.T) {
		apiPath1 := fmt.Sprintf("v1.0-alpha1/configuration/%s/subscribe", storeName)
		resp1 := fakeServer.DoRequest("GET", apiPath1, nil, nil)
		assert.Equal(t, 500, resp1.StatusCode, "subscribe configuration store, should return 500 when appchannel is not initialized")
		rspMap1 := resp1.JSONBody
		assert.Nil(t, rspMap1)

		uuid, err := uuid.NewRandom()
		require.NoError(t, err, "unable to generate id")
		apiPath2 := fmt.Sprintf("v1.0-alpha1/configuration/%s/%s/unsubscribe", "", &uuid)

		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)

		assert.Equal(t, gohttp.StatusNotFound, resp2.StatusCode, "Expected parameter store name can't be nil/empty")
	})

	t.Run("error in unsubscribe configurations", func(t *testing.T) {
		apiPath1 := fmt.Sprintf("v1.0/configuration/%s/subscribe", storeName)
		resp1 := fakeServer.DoRequest("GET", apiPath1, nil, nil)
		assert.Equal(t, 500, resp1.StatusCode, "subscribe configuration store, should return 500 when appchannel is not initialized")
		rspMap1 := resp1.JSONBody
		assert.Nil(t, rspMap1)

		uuid, err := uuid.NewRandom()
		require.NoError(t, err, "unable to generate id")
		apiPath2 := fmt.Sprintf("v1.0/configuration/%s/%s/unsubscribe", "", &uuid)

		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)

		assert.Equal(t, gohttp.StatusNotFound, resp2.StatusCode, "Expected parameter store name can't be nil/empty")
	})

	t.Run("error in unsubscribe configurations - alpha1", func(t *testing.T) {
		apiPath2 := fmt.Sprintf("v1.0-alpha1/configuration/%s/%s/unsubscribe", storeName, "subscribe_id_err")
		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)
		assert.Equal(t, 500, resp2.StatusCode, "Expected error during unsubscribe api")
		assert.NotNil(t, resp2.ErrorBody, "Unsubscribe configuration should return a non nil response body")
	})

	t.Run("error in unsubscribe configurations", func(t *testing.T) {
		apiPath2 := fmt.Sprintf("v1.0/configuration/%s/%s/unsubscribe", storeName, "subscribe_id_err")
		resp2 := fakeServer.DoRequest("GET", apiPath2, nil, nil)
		assert.Equal(t, 500, resp2.StatusCode, "Expected error during unsubscribe api")
		assert.NotNil(t, resp2.ErrorBody, "Unsubscribe configuration should return a non nil response body")
	})
}

func TestV1Alpha1DistributedLock(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	var fakeLockStore lock.Store = &fakeLockStore{}

	storeName := "store1"

	l := logger.NewLogger("fakeLogger")
	resiliencyConfig := resiliency.FromConfigurations(l, testResiliency)

	compStore := compstore.New()
	compStore.AddLock(storeName, fakeLockStore)
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     l,
			CompStore:  compStore,
			Resiliency: resiliencyConfig,
		}),
	}
	fakeServer.StartServer(testAPI.constructDistributedLockEndpoints(), nil)

	t.Run("Lock with valid request", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/lock/store1"

		req := lock.TryLockRequest{
			ResourceID:      "1",
			LockOwner:       "palpatine",
			ExpiryInSeconds: 5,
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 200, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.True(t, rspMap["success"].(bool))
	})

	t.Run("Lock with invalid resource id", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/lock/store1"

		req := lock.TryLockRequest{
			ResourceID:      "",
			LockOwner:       "palpatine",
			ExpiryInSeconds: 5,
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.Nil(t, resp.JSONBody)
	})

	t.Run("Lock with invalid owner", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/lock/store1"

		req := lock.TryLockRequest{
			ResourceID:      "1",
			LockOwner:       "",
			ExpiryInSeconds: 5,
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.Nil(t, resp.JSONBody)
	})

	t.Run("Lock with invalid expiry", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/lock/store1"

		req := lock.TryLockRequest{
			ResourceID: "1",
			LockOwner:  "palpatine",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.Nil(t, resp.JSONBody)
	})

	t.Run("Unlock with valid request", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/unlock/store1"

		req := lock.UnlockRequest{
			ResourceID: "1",
			LockOwner:  "palpatine",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 200, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]any)
		assert.NotNil(t, rspMap)
		assert.Equal(t, float64(0), rspMap["status"])
	})

	t.Run("Unlock with invalid resource id", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/unlock/store1"

		req := lock.UnlockRequest{
			ResourceID: "",
			LockOwner:  "palpatine",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.Contains(t, string(resp.RawBody), "ERR_MALFORMED_REQUEST")
		assert.Contains(t, string(resp.RawBody), "ResourceId is empty in lock store store1")
	})

	t.Run("Unlock with invalid resource id that returns 500", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/unlock/store1"

		req := lock.UnlockRequest{
			ResourceID: "error",
			LockOwner:  "palpatine",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 500, resp.StatusCode)

		// assert
		assert.Nil(t, resp.JSONBody)
	})

	t.Run("Unlock with invalid owner", func(t *testing.T) {
		apiPath := apiVersionV1alpha1 + "/unlock/store1"

		req := lock.UnlockRequest{
			ResourceID: "1",
			LockOwner:  "not-owner",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 200, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Equal(t, float64(3), rspMap["status"])
	})
}

func TestV1Beta1Workflow(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	fakeWorkflowComponent := &daprt.MockWorkflow{}

	componentName := "dapr"

	resiliencyConfig := resiliency.FromConfigurations(logger.NewLogger("workflow.test"), testResiliency)
	compStore := compstore.New()
	compStore.AddWorkflow(componentName, fakeWorkflowComponent)

	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}
	wfengine := wfengine.NewWorkflowEngine("testAppID", spec, nil)
	wfengine.SetWorkflowEngineReadyDone()

	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:         logger.NewLogger("fakeLogger"),
			CompStore:      compStore,
			Resiliency:     resiliencyConfig,
			WorkflowEngine: wfengine,
		}),
	}
	testAPI.universal.SetActorsInitDone()

	fakeServer.StartServer(testAPI.constructWorkflowEndpoints(), nil)

	/////////////////////
	// START API TESTS //
	/////////////////////

	t.Run("Start with non existent component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/workflowName/start"

		req := workflowContrib.StartRequest{
			WorkflowName: "Non-existent-workflow",
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Start with no instance ID", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/dapr/workflowName/start"
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert that we got a response back like:
		// {"instanceID": "some-random-value"}
		assert.Nil(t, resp.ErrorBody)
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Contains(t, rspMap, "instanceID")
		instanceID := rspMap["instanceID"].(string)
		assert.NotEmpty(t, instanceID) // the instance ID should be a non-empty, random value string (e.g. UUID)
	})

	t.Run("Start with invalid instance ID", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/dapr/workflowName/start?instanceID=invalid$ID"
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_INSTANCE_ID_INVALID", resp.ErrorBody["errorCode"])
		assert.Equal(t, messages.ErrInvalidInstanceID.WithFormat("invalid$ID").Message(), resp.ErrorBody["message"])
	})

	t.Run("Start with too long instance ID", func(t *testing.T) {
		maxInstanceIDLength := 64
		apiPath := "v1.0-beta1/workflows/dapr/workflowName/start?instanceID=this_is_a_very_long_instance_id_that_is_longer_than_64_characters_and_therefore_should_not_be_allowed"
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_INSTANCE_ID_TOO_LONG", resp.ErrorBody["errorCode"])
		assert.Equal(t, messages.ErrInstanceIDTooLong.WithFormat(maxInstanceIDLength).Message(), resp.ErrorBody["message"])
	})

	t.Run("Start with explicit instance ID", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/dapr/workflowName/start?instanceID=my-explicit-ID"
		resp := fakeServer.DoRequest("POST", apiPath, []byte("input payload"), nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert that we got a response back like:
		// {"instanceID": "my-explicit-ID"}
		assert.Nil(t, resp.ErrorBody)
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Contains(t, rspMap, "instanceID")
		instanceID := rspMap["instanceID"].(string)
		assert.Equal(t, "my-explicit-ID", instanceID) // the ID we provided should be returned
	})

	/////////////////////
	// GET API TESTS ////
	/////////////////////

	t.Run("Get with non existent workflow component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/instanceID"

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Get with valid api call", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'get' method implemented to return a dummy response.
		apiPath := "v1.0-beta1/workflows/dapr/myInstanceID"

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		assert.Equal(t, 200, resp.StatusCode)

		// assert that we get a response back like:
		// {"workflow": {"instanceID": "instanceID", "runtimeStatus": "RUNNING", "createdAt": "2023-04-08T15:30:00.123Z", "lastUpdatedAt": "2023-04-08T15:30:00.123Z"}}
		assert.Nil(t, resp.ErrorBody)
		assert.NotNil(t, resp.JSONBody)
		rspMap := resp.JSONBody.(map[string]interface{})
		assert.NotNil(t, rspMap)
		assert.Len(t, rspMap, 5) // check this in case we add more fields to the response
		assert.Contains(t, rspMap, "instanceID")
		assert.Equal(t, "myInstanceID", rspMap["instanceID"])
		assert.Contains(t, rspMap, "workflowName")
		assert.Equal(t, "mockWorkflowName", rspMap["workflowName"]) // The mock is designed to always return "mockWorkflowName" for workflow name
		assert.Contains(t, rspMap, "runtimeStatus")
		assert.Equal(t, "TESTING", rspMap["runtimeStatus"]) // the mock is designed to always return "TESTING" for runtime status
		assert.Contains(t, rspMap, "createdAt")
		createdAtStr := rspMap["createdAt"].(string)
		_, err := time.Parse(time.RFC3339, createdAtStr) // we expect timestamps to be in RFC3339 format
		require.NoError(t, err)
		assert.Contains(t, rspMap, "lastUpdatedAt")
		lastUpdatedAtStr := rspMap["lastUpdatedAt"].(string)
		_, err = time.Parse(time.RFC3339, lastUpdatedAtStr) // we expect timestamps to be in RFC3339 format
		require.NoError(t, err)
	})

	/////////////////////////
	// TERMINATE API TESTS //
	/////////////////////////

	t.Run("Terminate with non existent component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/instanceID/terminate"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Terminate with valid API path", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'terminate' method implemented to simply return nil

		apiPath := "v1.0-beta1/workflows/dapr/instanceID/terminate"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert
		assert.Nil(t, resp.ErrorBody)
	})

	///////////////////////////
	// RAISE EVENT API TESTS //
	///////////////////////////

	t.Run("Raise Event with non existent component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/instanceID/raiseEvent/fakeEvent"

		req := workflowContrib.RaiseEventRequest{
			InstanceID: "",
			EventName:  "",
			EventData:  nil,
		}

		b, _ := json.Marshal(&req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Raise Event with valid API path", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'RaiseEvent' method implemented to simply return nil

		apiPath := "v1.0-beta1/workflows/dapr/instanceID/raiseEvent/fakeEvent"

		resp := fakeServer.DoRequest("POST", apiPath, []byte("event payload"), nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert
		assert.Nil(t, resp.ErrorBody)
	})

	/////////////////////////
	// PAUSE API TESTS //
	/////////////////////////

	t.Run("Pause with non existent component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/instanceID/pause"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Pause with valid API path", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'pause' method implemented to simply return nil

		apiPath := "v1.0-beta1/workflows/dapr/instanceID/pause"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert
		assert.Nil(t, resp.ErrorBody)
	})

	/////////////////////////
	// RESUME API TESTS //
	/////////////////////////

	t.Run("Resume with non existent component", func(t *testing.T) {
		apiPath := "v1.0-beta1/workflows/non-existent-component/instanceID/resume"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 400, resp.StatusCode)

		// assert
		assert.NotNil(t, resp.ErrorBody)
		assert.Equal(t, "ERR_WORKFLOW_COMPONENT_NOT_FOUND", resp.ErrorBody["errorCode"])
		assert.Equal(t, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist.Message(), "non-existent-component"), resp.ErrorBody["message"])
	})

	t.Run("Resume with valid API path", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'resume' method implemented to simply return nil

		apiPath := "v1.0-beta1/workflows/dapr/instanceID/resume"

		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert
		assert.Nil(t, resp.ErrorBody)
	})

	/////////////////////
	// PURGE API TESTS //
	/////////////////////

	t.Run("Purge with valid API path", func(t *testing.T) {
		// Note that this test passes even though there is no workflow implemented.
		// This is due to the fact that the 'fakecomponent' has the 'purge' method implemented to simply return nil

		apiPath := "v1.0-beta1/workflows/dapr/instanceID/purge"
		resp := fakeServer.DoRequest("POST", apiPath, nil, nil)
		assert.Equal(t, 202, resp.StatusCode)

		// assert
		assert.Nil(t, resp.ErrorBody)
	})
}

func buildHTTPPipeline(spec config.PipelineSpec) middleware.HTTP {
	h := middlewarehttp.New()
	h.Add(middlewarehttp.Spec{
		Component: componentsV1alpha1.Component{
			ObjectMeta: metaV1.ObjectMeta{Name: "middleware.http.uppercase"},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		},
		Implementation: utils.UppercaseRequestMiddleware,
	})

	return h.BuildPipelineFromSpec("test", &spec)
}

func TestSinglePipelineWithTracer(t *testing.T) {
	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataString("fakeDirectMessageResponse").
		WithContentType("application/json")
	defer fakeDirectMessageResponse.Close()

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1.0"}

	pipeline := buildHTTPPipeline(config.PipelineSpec{
		Handlers: []config.HandlerSpec{
			{
				Type: "middleware.http.uppercase",
				Name: "middleware.http.uppercase",
			},
		},
	})

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
		spec:     &spec,
		pipeline: pipeline,
	})

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
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
}

func TestSinglePipelineWithNoTracing(t *testing.T) {
	fakeDirectMessageResponse := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataString("fakeDirectMessageResponse").
		WithContentType("application/json")
	defer fakeDirectMessageResponse.Close()

	mockDirectMessaging := new(daprt.MockDirectMessaging)

	fakeServer := newFakeHTTPServer()

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "0"}

	pipeline := buildHTTPPipeline(config.PipelineSpec{
		Handlers: []config.HandlerSpec{
			{
				Type: "middleware.http.uppercase",
				Name: "middleware.http.uppercase",
			},
		},
	})

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
		spec:     &spec,
		pipeline: pipeline,
	})

	t.Run("Invoke direct messaging without querystring - 200 OK", func(t *testing.T) {
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
		assert.Equal(t, "", buffer, "failed to generate proper traces with invoke")
		assert.Equal(t, 200, resp.StatusCode)
	})
}

// Fake http server and client helpers to simplify endpoints test.
func newFakeHTTPServer() *fakeHTTPServer {
	return &fakeHTTPServer{}
}

type fakeHTTPServer struct {
	ln     *bufconn.Listener
	client gohttp.Client
}

type fakeHTTPResponse struct {
	StatusCode  int
	ContentType string
	RawHeader   gohttp.Header
	RawBody     []byte
	JSONBody    interface{}
	ErrorBody   map[string]string
}

type fakeHTTPServerOptions struct {
	spec     *config.TracingSpec
	pipeline middleware.HTTP
	apiAuth  bool
}

func (f *fakeHTTPServer) StartServer(endpoints []endpoints.Endpoint, opts *fakeHTTPServerOptions) {
	if opts == nil {
		opts = &fakeHTTPServerOptions{}
	}

	f.ln = bufconn.Listen(bufconnBufSize)

	r := f.getRouter(endpoints, opts.apiAuth)
	go func() {
		var handler gohttp.Handler = r
		if opts.pipeline != nil {
			handler = opts.pipeline(handler)
		}
		if opts.spec != nil {
			handler = diag.HTTPTraceMiddleware(handler, "fakeAppID", *opts.spec)
		}
		//nolint:gosec
		err := gohttp.Serve(f.ln, handler)
		if err != nil && err.Error() != "closed" {
			panic(fmt.Errorf("failed to start server: %v", err))
		}
	}()

	f.client = gohttp.Client{
		Transport: &gohttp.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return f.ln.DialContext(ctx)
			},
		},
	}
}

func (f *fakeHTTPServer) getRouter(endpoints []endpoints.Endpoint, apiAuth bool) chi.Router {
	srv := &server{}

	r := srv.getRouter()

	if apiAuth {
		srv.useAPIAuthentication(r)
	}

	for _, e := range endpoints {
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)

		srv.handle(e, path, r, false)
	}
	return r
}

func (f *fakeHTTPServer) Shutdown() {
	f.ln.Close()
}

func (f *fakeHTTPServer) DoRequestWithAPIToken(method, path, token string, body []byte) fakeHTTPResponse {
	url := fmt.Sprintf("http://127.0.0.1/%s", path)
	r, _ := gohttp.NewRequest(method, url, bytes.NewBuffer(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("dapr-api-token", token)
	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	res.Body.Close()
	response := fakeHTTPResponse{
		StatusCode:  res.StatusCode,
		ContentType: res.Header.Get("Content-Type"),
		RawHeader:   res.Header,
	}
	return response
}

func (f *fakeHTTPServer) doRequest(basicAuth, method, path string, body []byte, params map[string]string, headers ...string) fakeHTTPResponse {
	url := fmt.Sprintf("http://127.0.0.1/%s", path)
	if basicAuth != "" {
		url = fmt.Sprintf("http://%s@127.0.0.1/%s", basicAuth, path)
	}

	if params != nil {
		url += "?"
		for k, v := range params {
			url += k + "=" + v + "&"
		}
		url = url[:len(url)-1]
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := gohttp.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	for i := 0; i < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}

	res, err := f.client.Do(r)
	if err != nil {
		panic(fmt.Errorf("failed to request: %v", err))
	}

	bodyBytes, _ := io.ReadAll(res.Body)
	defer res.Body.Close()
	response := fakeHTTPResponse{
		StatusCode:  res.StatusCode,
		ContentType: res.Header.Get("Content-Type"),
		RawHeader:   res.Header,
		RawBody:     bodyBytes,
	}

	if response.ContentType == "application/json" {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			json.Unmarshal(bodyBytes, &response.JSONBody)
		} else {
			json.Unmarshal(bodyBytes, &response.ErrorBody)
		}
	}

	return response
}

func (f *fakeHTTPServer) DoRequest(method, path string, body []byte, params map[string]string, headers ...string) fakeHTTPResponse {
	return f.doRequest("", method, path, body, params, headers...)
}

func TestV1StateEndpoints(t *testing.T) {
	etag := "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"
	fakeServer := newFakeHTTPServer()
	var fakeStore state.Store = newFakeStateStoreQuerier()
	failingStore := &daprt.FailingStatestore{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingGetKey":        1,
				"failingSetKey":        1,
				"failingDeleteKey":     1,
				"failingBulkGetKey":    1,
				"failingBulkSetKey":    1,
				"failingBulkDeleteKey": 1,
				"failingMultiKey":      1,
				"failingQueryKey":      1,
			},
			map[string]time.Duration{
				"timeoutGetKey":         time.Second * 30,
				"timeoutSetKey":         time.Second * 30,
				"timeoutDeleteKey":      time.Second * 30,
				"timeoutBulkGetKey":     time.Second * 30,
				"timeoutBulkGetKeyBulk": time.Second * 30,
				"timeoutBulkSetKey":     time.Second * 30,
				"timeoutBulkDeleteKey":  time.Second * 30,
				"timeoutMultiKey":       time.Second * 30,
				"timeoutQueryKey":       time.Second * 30,
			},
			map[string]int{},
		),
	}
	const storeName = "store1"
	compStore := compstore.New()
	compStore.AddStateStore(storeName, fakeStore)
	compStore.AddStateStore("failStore", failingStore)
	rc := resiliency.FromConfigurations(logger.NewLogger("state.test"), testResiliency)
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("fakeLogger"),
			CompStore:  compStore,
			Resiliency: rc,
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{},
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints(), nil)

	t.Run("Get state - 400 ERR_STATE_STORE_NOT_FOUND or NOT_CONFIGURED", func(t *testing.T) {
		apisAndMethods := map[string][]string{
			"v1.0/state/nonexistantStore/bad-key":      {"GET", "DELETE"},
			"v1.0/state/nonexistantStore/":             {"POST", "PUT"},
			"v1.0/state/nonexistantStore/bulk":         {"POST", "PUT"},
			"v1.0/state/nonexistantStore/transaction":  {"POST", "PUT"},
			"v1.0-alpha1/state/nonexistantStore/query": {"POST", "PUT"},
		}

		for apiPath, testMethods := range apisAndMethods {
			for _, method := range testMethods {
				for name := range testAPI.universal.CompStore().ListStateStores() {
					testAPI.universal.CompStore().DeleteStateStore(name)
				}
				resp := fakeServer.DoRequest(method, apiPath, nil, nil)
				// assert
				assert.Equal(t, 500, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_STATE_STORE_NOT_CONFIGURED", resp.ErrorBody["errorCode"])

				testAPI.universal.CompStore().AddStateStore("store1", fakeStore)
				testAPI.universal.CompStore().AddStateStore("failStore", failingStore)

				// act
				resp = fakeServer.DoRequest(method, apiPath, nil, nil)
				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_STATE_STORE_NOT_FOUND", resp.ErrorBody["errorCode"], apiPath)
			}
		}
	})

	t.Run("State PUT/POST APIs - 400 invalid JSON request", func(t *testing.T) {
		apiPaths := []string{
			"v1.0/state/store1/",
			"v1.0/state/store1/bulk",
			"v1.0/state/store1/transaction",
		}

		for _, apiPath := range apiPaths {
			for _, requestMethod := range []string{"PUT", "POST"} {
				// {{
				inputBodyBytes := invalidJSON

				// act
				resp := fakeServer.DoRequest(requestMethod, apiPath, inputBodyBytes, nil)

				// assert
				assert.Equal(t, 400, resp.StatusCode, apiPath)
				assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"], apiPath)
			}
		}
	})

	t.Run("Get state - 204 No Content Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bad-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "reading non-existing key should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Get state - Good Key", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, etag, resp.RawHeader.Get("ETag"), "failed to read etag")
	})

	t.Run("Get state - Upstream error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/error-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, "ERR_STATE_GET", resp.ErrorBody["errorCode"])
	})

	t.Run("Update state - PUT verb supported", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key: "good-key",
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating the state store with the PUT verb should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key: "good-key",
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key without etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - State Error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "error-key",
			ETag: ptr.Of(""),
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "state error should return 500 status")
		assert.Equal(t, "ERR_STATE_SAVE", resp.ErrorBody["errorCode"])
	})

	t.Run("Update state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: &etag,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key with matching etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Update state - Wrong ETag", func(t *testing.T) {
		invalidEtag := "BAD ETAG"
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{{
			Key:  "good-key",
			ETag: &invalidEtag,
		}}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 409, resp.StatusCode, "updating existing key with wrong etag should fail")
	})

	t.Run("Update bulk state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{
			{Key: "good-key"},
			{Key: "good-key2"},
		}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, "", string(resp.RawBody))
	})

	t.Run("Update bulk state - State Error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{
			{Key: "good-key"},
			{Key: "error-key"},
		}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, "ERR_STATE_SAVE", resp.ErrorBody["errorCode"])
	})

	t.Run("Update bulk state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{
			{Key: "good-key", ETag: &etag},
			{Key: "good-key2", ETag: &etag},
		}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, "", string(resp.RawBody))
	})

	t.Run("Update bulk state - One has invalid ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", storeName)
		request := []state.SetRequest{
			{Key: "good-key", ETag: &etag},
			{Key: "good-key2", ETag: ptr.Of("BAD ETAG")},
		}
		b, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		// assert
		assert.Equal(t, 409, resp.StatusCode)
	})

	t.Run("Delete state - No ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key without etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Delete state - Matching ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "If-Match", etag)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "updating existing key with matching etag should succeed")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Delete state - Bad ETag", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil, "If-Match", "BAD ETAG")
		// assert
		assert.Equal(t, 409, resp.StatusCode, "updating existing key with wrong etag should fail")
	})

	t.Run("Bulk state get - Empty request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on an empty body")
	})

	t.Run("Bulk state get - PUT request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on an empty body")
	})

	t.Run("Bulk state get - normal request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{
			Keys: []string{"good-key", "foo"},
		}
		body, _ := json.Marshal(request)

		// act

		resp := fakeServer.DoRequest("POST", apiPath, body, nil)

		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed on a normal request")

		var responses []BulkGetResponse

		require.NoError(t, json.Unmarshal(resp.RawBody, &responses), "Response should be valid JSON")

		expectedResponses := []BulkGetResponse{
			{
				Key:   "good-key",
				Data:  json.RawMessage("\"bGlmZSBpcyBnb29k\""),
				ETag:  ptr.Of("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
				Error: "",
			},
			{
				Key:   "foo",
				Data:  nil,
				ETag:  nil,
				Error: "",
			},
		}

		assert.Equal(t, expectedResponses, responses, "Responses do not match")
	})

	t.Run("Bulk state get - one key returns error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", storeName)
		request := BulkGetRequest{
			Keys: []string{"good-key", "error-key"},
		}
		body, _ := json.Marshal(request)
		// act
		resp := fakeServer.DoRequest("POST", apiPath, body, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "Bulk API should succeed even if key not found")

		var responses []BulkGetResponse

		require.NoError(t, json.Unmarshal(resp.RawBody, &responses), "Response should be valid JSON")

		expectedResponses := []BulkGetResponse{
			{
				Key:   "good-key",
				Data:  json.RawMessage("\"bGlmZSBpcyBnb29k\""),
				ETag:  ptr.Of("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
				Error: "",
			},
			{
				Key:   "error-key",
				Data:  nil,
				ETag:  nil,
				Error: "UPSTREAM STATE ERROR",
			},
		}

		assert.Equal(t, expectedResponses, responses, "Responses do not match")
	})

	t.Run("Query state request", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/state/%s/query", storeName)
		// act
		resp := fakeServer.DoRequest("PUT", apiPath, []byte(queryTestRequestOK), nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestOK), nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestNoRes), nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestErr), nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode)
		// act
		resp = fakeServer.DoRequest("POST", apiPath, []byte(queryTestRequestSyntaxErr), nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode)
	})

	t.Run("get state request retries with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/failingGetKey", "failStore")

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		assert.Equal(t, 204, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingGetKey"))
	})

	t.Run("get state request times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/timeoutGetKey", "failStore")

		start := time.Now()
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutGetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("set state request retries with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", "failStore")

		request := []state.SetRequest{{
			Key: "failingSetKey",
		}}
		b, _ := json.Marshal(request)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		assert.Equal(t, 204, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingSetKey"))
	})

	t.Run("set state request times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", "failStore")

		request := []state.SetRequest{{
			Key: "timeoutSetKey",
		}}
		b, _ := json.Marshal(request)

		start := time.Now()
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutSetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("delete state request retries with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/failingDeleteKey", "failStore")

		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)
		assert.Equal(t, 204, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingDeleteKey"))
	})

	t.Run("delete state request times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/timeoutDeleteKey", "failStore")

		start := time.Now()
		resp := fakeServer.DoRequest("DELETE", apiPath, nil, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode) // No body in the response.
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutDeleteKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("bulk state get fails with bulk support", func(t *testing.T) {
		// Adding this will make the bulk operation fail
		failingStore.BulkFailKey.Store(ptr.Of("timeoutBulkGetKeyBulk"))
		t.Cleanup(func() {
			failingStore.BulkFailKey.Store(ptr.Of(""))
		})

		apiPath := fmt.Sprintf("v1.0/state/%s/bulk", "failStore")
		request := BulkGetRequest{
			Keys: []string{"failingBulkGetKey", "goodBulkGetKey"},
		}
		body, _ := json.Marshal(request)

		resp := fakeServer.DoRequest("POST", apiPath, body, nil)

		assert.Equal(t, gohttp.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("bulk state set recovers from single key failure with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", "failStore")

		reqs := []state.SetRequest{
			{
				Key: "failingBulkSetKey",
			},
			{
				Key: "goodBulkSetKey",
			},
		}
		b, _ := json.Marshal(reqs)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)

		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingBulkSetKey"))
		assert.Equal(t, 1, failingStore.Failure.CallCount("goodBulkSetKey"))
	})

	t.Run("bulk state set times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s", "failStore")

		reqs := []state.SetRequest{
			{
				Key: "timeoutBulkSetKey",
			},
			{
				Key: "goodTimeoutBulkSetKey",
			},
		}
		b, _ := json.Marshal(reqs)

		start := time.Now()
		resp := fakeServer.DoRequest("POST", apiPath, b, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutBulkSetKey"))
		assert.Equal(t, 0, failingStore.Failure.CallCount("goodTimeoutBulkSetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("state transaction passes after retries with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", "failStore")

		req := &stateTransactionRequestBody{
			Operations: []stateTransactionRequestBodyOperation{
				{
					Operation: string(state.OperationDelete),
					Request: map[string]string{
						"key": "failingMultiKey",
					},
				},
			},
		}
		b, _ := json.Marshal(req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)

		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingMultiKey"))
	})

	t.Run("state transaction times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", "failStore")

		req := &stateTransactionRequestBody{
			Operations: []stateTransactionRequestBodyOperation{
				{
					Operation: string(state.OperationDelete),
					Request: map[string]string{
						"key": "timeoutMultiKey",
					},
				},
			},
		}
		b, _ := json.Marshal(req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutMultiKey"))
	})

	t.Run("state query retries with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/state/%s/query?metadata.key=failingQueryKey", "failStore")

		req := &state.QueryRequest{}
		b, _ := json.Marshal(req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)

		assert.Equal(t, 204, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingQueryKey"))
	})

	t.Run("state query times out with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0-alpha1/state/%s/query?metadata.key=timeoutQueryKey", "failStore")

		req := &state.QueryRequest{}
		b, _ := json.Marshal(req)

		resp := fakeServer.DoRequest("POST", apiPath, b, nil)

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutQueryKey"))
	})
}

func TestStateStoreQuerierNotImplemented(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	compStore := compstore.New()
	compStore.AddStateStore("store1", newFakeStateStore())
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("fakeLogger"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints(), nil)

	resp := fakeServer.DoRequest("POST", "v1.0-alpha1/state/store1/query", nil, nil)
	// assert
	assert.Equal(t, 500, resp.StatusCode)
	assert.Equal(t, "ERR_STATE_STORE_NOT_SUPPORTED", resp.ErrorBody["errorCode"])
}

func TestStateStoreQuerierNotEnabled(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	compStore := compstore.New()
	compStore.AddStateStore("store1", newFakeStateStore())
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("fakeLogger"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints(), nil)

	resp := fakeServer.DoRequest("POST", "v1.0/state/store1/query", nil, nil)
	// assert
	assert.Equal(t, 405, resp.StatusCode)
}

func TestStateStoreQuerierEncrypted(t *testing.T) {
	storeName := "encrypted-store1"
	fakeServer := newFakeHTTPServer()
	compStore := compstore.New()
	compStore.AddStateStore(storeName, newFakeStateStoreQuerier())
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("fakeLogger"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	encryption.AddEncryptedStateStore(storeName, encryption.ComponentEncryptionKeys{})
	fakeServer.StartServer(testAPI.constructStateEndpoints(), nil)

	resp := fakeServer.DoRequest("POST", "v1.0-alpha1/state/"+storeName+"/query", nil, nil)
	// assert
	assert.Equal(t, 500, resp.StatusCode)
	assert.Contains(t, string(resp.RawBody), "cannot query encrypted store")
}

const (
	queryTestRequestOK = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"sort": [
		{ "key": "a" }
	],
	"page": {
		"limit": 2
	}
}`
	queryTestRequestNoRes = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"page": {
		"limit": 2
	}
}`
	queryTestRequestErr = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"sort": [
		{ "key": "a" }
	]
}`
	queryTestRequestSyntaxErr = `syntax error`
)

type fakeStateStore struct {
	state.BulkStore
}

func newFakeStateStore() fakeStateStore {
	s := fakeStateStore{}
	s.BulkStore = state.NewDefaultBulkStore(s)
	return s
}

func (c fakeStateStore) Ping() error {
	return nil
}

func (c fakeStateStore) Delete(ctx context.Context, req *state.DeleteRequest) error {
	if req.Key == "good-key" {
		if req.ETag != nil && *req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return state.NewETagError(state.ETagMismatch, errors.New("ETag mismatch"))
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	if req.Key == "good-key" {
		return &state.GetResponse{
			Data: []byte("\"bGlmZSBpcyBnb29k\""),
			ETag: ptr.Of("`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'"),
		}, nil
	}
	if req.Key == "error-key" {
		return nil, errors.New("UPSTREAM STATE ERROR")
	}
	return nil, nil
}

func (c fakeStateStore) Init(ctx context.Context, metadata state.Metadata) error {
	return nil
}

func (c fakeStateStore) Features() []state.Feature {
	return []state.Feature{
		state.FeatureETag,
		state.FeatureTransactional,
	}
}

func (c fakeStateStore) Set(ctx context.Context, req *state.SetRequest) error {
	if req.Key == "good-key" || req.Key == "good-key2" {
		if req.ETag != nil && *req.ETag != "`~!@#$%^&*()_+-={}[]|\\:\";'<>?,./'" {
			return state.NewETagError(state.ETagMismatch, errors.New("ETag mismatch"))
		}
		return nil
	}
	return errors.New("NOT FOUND")
}

func (c fakeStateStore) Multi(ctx context.Context, request *state.TransactionalStateRequest) error {
	if request.Metadata != nil && request.Metadata["error"] == "true" {
		return errors.New("Transaction error")
	}
	return nil
}

type fakeStateStoreQuerier struct {
	state.Store
	state.TransactionalStore
}

func (c fakeStateStoreQuerier) MultiMaxSize() int {
	return 10
}

func newFakeStateStoreQuerier() fakeStateStoreQuerier {
	s := newFakeStateStore()
	return fakeStateStoreQuerier{
		Store:              s,
		TransactionalStore: s,
	}
}

func (c fakeStateStoreQuerier) Query(ctx context.Context, req *state.QueryRequest) (*state.QueryResponse, error) {
	// simulate empty data
	if req.Query.Sort == nil {
		return &state.QueryResponse{}, nil
	}
	// simulate error
	if req.Query.Page.Limit == 0 {
		return nil, errors.New("Query error")
	}
	// simulate full result
	return &state.QueryResponse{
		Results: []state.QueryItem{
			{
				Key:  "1",
				Data: []byte(`{"a":"b"}`),
			},
		},
	}, nil
}

func TestV1SecretEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	fakeStore := daprt.FakeSecretStore{}
	failingStore := daprt.FailingSecretStore{
		Failure: daprt.NewFailure(
			map[string]int{"key": 1, "bulk": 1},
			map[string]time.Duration{"timeout": time.Second * 30, "bulkTimeout": time.Second * 30},
			map[string]int{},
		),
	}
	fakeStores := map[string]secretstores.SecretStore{
		"store1":     fakeStore,
		"store2":     fakeStore,
		"store3":     fakeStore,
		"store4":     fakeStore,
		"failSecret": failingStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
		"store2": {
			DefaultAccess:  config.DenyAccess,
			AllowedSecrets: []string{"good-key"},
		},
		"store3": {
			DefaultAccess:  config.AllowAccess,
			AllowedSecrets: []string{"good-key"},
		},
	}

	l := logger.NewLogger("fakeLogger")
	res := resiliency.FromConfigurations(l, testResiliency)

	compStore := compstore.New()
	for name, conf := range secretsConfiguration {
		compStore.AddSecretsConfiguration(name, conf)
	}
	for name, store := range fakeStores {
		compStore.AddSecretStore(name, store)
	}
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     l,
			CompStore:  compStore,
			Resiliency: res,
		}),
	}
	fakeServer.StartServer(testAPI.constructSecretsEndpoints(), nil)
	storeName := "store1"
	deniedStoreName := "store2"
	restrictedStore := "store3"
	unrestrictedStore := "store4" // No configuration defined for the store

	t.Run("Get secret - 401 ERR_SECRET_STORE_NOT_FOUND", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bad-key", "notexistStore")
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 401, resp.StatusCode, "reading non-existing store should return 401")
	})

	t.Run("Get secret - 204 No Content Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bad-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 204, resp.StatusCode, "reading non-existing key should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/not-allowed", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading not allowed key should return 403")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/random", deniedStoreName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading random key from store with default deny access should return 403")
	})

	t.Run("Get secret - 403 Permission denied ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/random", restrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 403, resp.StatusCode, "reading random key from store with restricted allow access should return 403")
	})

	t.Run("Get secret - 200 Good Ket restricted store ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", restrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading good-key key from store with restricted allow access should return 200")
	})

	t.Run("Get secret - 200 Good Key allowed access ", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", deniedStoreName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading allowed good-key key from store with default deny access should return 200")
	})

	t.Run("Get secret - Good Key default allow", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
	})

	t.Run("Get secret - Good Key from unrestricted store", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", unrestrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading existing key should succeed")
	})

	t.Run("Get secret - 500 for upstream error", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/error-key", unrestrictedStore)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading existing key should succeed")
		assert.Equal(t, "ERR_SECRET_GET", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Get secret - 500 for secret store not congfigured", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/good-key", unrestrictedStore)
		// act
		for name := range testAPI.universal.CompStore().ListSecretStores() {
			testAPI.universal.CompStore().DeleteSecretStore(name)
		}
		defer func() {
			for name, store := range fakeStores {
				testAPI.universal.CompStore().AddSecretStore(name, store)
			}
		}()

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 500, resp.StatusCode, "reading from not-configured secret store should fail with 500")
		assert.Equal(t, "ERR_SECRET_STORES_NOT_CONFIGURED", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Get Bulk secret - Good Key default allow", func(t *testing.T) {
		// The interface{} use here is due to JSONBody usage
		expectedOutput := map[string]interface{}{
			"good-key": map[string]interface{}{"good-key": "life is good"},
		}
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bulk", storeName)
		// act
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		// assert
		assert.Equal(t, 200, resp.StatusCode, "reading secrets should succeed")
		body := resp.JSONBody
		assert.Equal(t, expectedOutput, body, "bulk secret response should be same as expected")
	})

	t.Run("Get secret - retries on initial failure with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/key", "failSecret")

		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("key"))
	})

	t.Run("Get secret - timeout before request ends", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/timeout", "failSecret")

		// Store sleeps for 30 seconds, let's make sure our timeout takes less time than that.
		start := time.Now()
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeout"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("Get bulk secret - retries on initial failure with resiliency", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bulk", "failSecret")

		resp := fakeServer.DoRequest("GET", apiPath, nil, map[string]string{"metadata.key": "bulk"})

		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulk"))
	})

	t.Run("Get bulk secret - timeout before request ends", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/secrets/%s/bulk", "failSecret")

		start := time.Now()
		resp := fakeServer.DoRequest("GET", apiPath, nil, map[string]string{"metadata.key": "bulkTimeout"})
		end := time.Now()

		assert.Equal(t, 500, resp.StatusCode)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulkTimeout"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})
}

type fakeConfigurationStore struct {
	counter int
}

func (c fakeConfigurationStore) Ping() error {
	return nil
}

func (c fakeConfigurationStore) Get(ctx context.Context, req *configuration.GetRequest) (*configuration.GetResponse, error) {
	if len(req.Keys) == 0 {
		return &configuration.GetResponse{
			Items: map[string]*configuration.Item{
				"good-key1": {
					Value:   "good-value1",
					Version: "version1",
					Metadata: map[string]string{
						"metadata-key1": "metadata-value1",
					},
				},
				"good-key2": {
					Value:   "good-value2",
					Version: "version2",
					Metadata: map[string]string{
						"metadata-key2": "metadata-value2",
					},
				},
			},
		}, nil
	}

	if len(req.Keys) == 1 && req.Keys[0] == "good-key1" {
		return &configuration.GetResponse{
			Items: map[string]*configuration.Item{
				"good-key1": {
					Value:   "good-value1",
					Version: "version1",
					Metadata: map[string]string{
						"metadata-key1": "metadata-value1",
					},
				},
			},
		}, nil
	}

	if len(req.Keys) == 2 && req.Keys[0] == "good-key1" && req.Keys[1] == "good-key2" {
		return &configuration.GetResponse{
			Items: map[string]*configuration.Item{
				"good-key1": {
					Value:   "good-value1",
					Version: "version1",
					Metadata: map[string]string{
						"metadata-key1": "metadata-value1",
					},
				}, "good-key2": {
					Value:   "good-value2",
					Version: "version2",
					Metadata: map[string]string{
						"metadata-key2": "metadata-value2",
					},
				},
			},
		}, nil
	}

	if req.Keys[0] == "bad-key" {
		return nil, errors.New("get key error: bad-key")
	}

	return nil, errors.New("get key error: value not found")
}

func (c fakeConfigurationStore) Init(ctx context.Context, metadata configuration.Metadata) error {
	c.counter = 0 //nolint:staticcheck
	return nil
}

func (c *fakeConfigurationStore) Subscribe(ctx context.Context, req *configuration.SubscribeRequest, handler configuration.UpdateHandler) (string, error) {
	return "", nil
}

func (c *fakeConfigurationStore) Unsubscribe(ctx context.Context, req *configuration.UnsubscribeRequest) error {
	if req.ID == "subscribe_id_err" {
		return errors.New("Error occurred during unsubscribe op")
	}
	return nil
}

type fakeLockStore struct{}

func (l fakeLockStore) Ping() error {
	return nil
}

func (l *fakeLockStore) InitLockStore(ctx context.Context, metadata lock.Metadata) error {
	return nil
}

func (l *fakeLockStore) TryLock(ctx context.Context, req *lock.TryLockRequest) (*lock.TryLockResponse, error) {
	if req == nil {
		return &lock.TryLockResponse{
			Success: false,
		}, errors.New("empty request")
	}

	if req.ExpiryInSeconds == 0 {
		return &lock.TryLockResponse{
			Success: false,
		}, errors.New("invalid expiry")
	}

	if req.LockOwner == "" {
		return &lock.TryLockResponse{
			Success: false,
		}, errors.New("invalid lockOwner")
	}

	if req.ResourceID == "lock||" {
		return &lock.TryLockResponse{
			Success: false,
		}, errors.New("invalid resourceId")
	}

	return &lock.TryLockResponse{
		Success: true,
	}, nil
}

func (l *fakeLockStore) Unlock(ctx context.Context, req *lock.UnlockRequest) (*lock.UnlockResponse, error) {
	if req == nil {
		return &lock.UnlockResponse{}, errors.New("empty request")
	}

	if req.LockOwner == "not-owner" {
		return &lock.UnlockResponse{
			Status: 3,
		}, nil
	}

	if req.ResourceID == "lock||" {
		return &lock.UnlockResponse{
			Status: 3,
		}, nil
	}

	if req.ResourceID == "lock||error" {
		return &lock.UnlockResponse{}, errors.New("error")
	}

	return &lock.UnlockResponse{
		Status: 0,
	}, nil
}

func TestV1HealthzEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	const appID = "fakeAPI"
	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID: appID,
		}),
	}

	fakeServer.StartServer(testAPI.constructHealthzEndpoints(), nil)

	t.Run("Healthz - 500 ERR_HEALTH_NOT_READY", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 500, resp.StatusCode, "dapr not ready should return 500")
	})

	t.Run("Healthz - 204 No Content", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		testAPI.MarkStatusAsReady()
		resp := fakeServer.DoRequest("GET", apiPath, nil, nil)

		assert.Equal(t, 204, resp.StatusCode)
	})

	t.Run("Healthz - 500 No AppId Match", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		testAPI.MarkStatusAsReady()
		resp := fakeServer.DoRequest("GET", apiPath, nil, map[string]string{"appid": "not-test"})
		assert.Equal(t, 500, resp.StatusCode)
	})

	t.Run("Healthz - 204 AppId Match", func(t *testing.T) {
		apiPath := "v1.0/healthz"
		testAPI.MarkStatusAsReady()
		resp := fakeServer.DoRequest("GET", apiPath, nil, map[string]string{"appid": appID})
		assert.Equal(t, 204, resp.StatusCode)
	})

	fakeServer.Shutdown()
}

func TestV1TransactionEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()
	var fakeStore state.Store = newFakeStateStoreQuerier()
	fakeStoreNonTransactional := new(daprt.MockStateStore)
	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)
	compStore.AddStateStore("storeNonTransactional", fakeStoreNonTransactional)

	testAPI := &api{
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{},
	}
	fakeServer.StartServer(testAPI.constructStateEndpoints(), nil)
	fakeBodyObject := map[string]interface{}{"data": "fakeData"}
	storeName := "store1"
	nonTransactionalStoreName := "storeNonTransactional"

	t.Run("Direct Transaction - 204 No Content", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []stateTransactionRequestBodyOperation{
			{
				Operation: string(state.OperationUpsert),
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: string(state.OperationDelete),
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
		})

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 204, resp.StatusCode, "Dapr should return 204")
		assert.Equal(t, []byte{}, resp.RawBody, "Always give empty body with 204")
	})

	t.Run("Post non-existent state store - 400 No State Store Found", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", "non-existent-store")
		testTransactionalOperations := []stateTransactionRequestBodyOperation{
			{
				Operation: string(state.OperationUpsert),
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: string(state.OperationDelete),
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
		})
		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)
		// assert
		assert.Equal(t, 400, resp.StatusCode, "Accessing non-existent state store should return 400")
	})

	t.Run("Invalid opperation - 400 ERR_NOT_SUPPORTED_STATE_OPERATION", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []stateTransactionRequestBodyOperation{
			{
				Operation: "foo",
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
		})

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 400, resp.StatusCode, "Dapr should return 400")
		assert.Equal(t, "ERR_NOT_SUPPORTED_STATE_OPERATION", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Invalid request obj - 400 ERR_MALFORMED_REQUEST", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		for _, operation := range []state.OperationType{state.OperationUpsert, state.OperationDelete} {
			testTransactionalOperations := []stateTransactionRequestBodyOperation{
				{
					Operation: string(operation),
					Request: map[string]interface{}{
						// Should cause the decorder to fail
						"key":   []string{"fakeKey1"},
						"value": fakeBodyObject,
					},
				},
			}

			// act
			inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
				Operations: testTransactionalOperations,
			})

			require.NoError(t, err)
			resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

			// assert
			assert.Equal(t, 400, resp.StatusCode, "Dapr should return 400")
			assert.Equal(t, "ERR_MALFORMED_REQUEST", resp.ErrorBody["errorCode"], apiPath)
		}
	})

	t.Run("Too many transactions for state store - 400 ERR_MALFORMED_REQUEST", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)

		testTransactionalOperations := make([]stateTransactionRequestBodyOperation, 20)
		for i := 0; i < 20; i++ {
			testTransactionalOperations[i] = stateTransactionRequestBodyOperation{
				Operation: string(state.OperationUpsert),
				Request: map[string]any{
					"key":   fmt.Sprintf("key%d", i),
					"value": fakeBodyObject,
				},
			}
		}

		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
		})

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, gohttp.StatusBadRequest, resp.StatusCode, "Dapr should return 400")
		assert.Equal(t, "ERR_STATE_STORE_TOO_MANY_TRANSACTIONS", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Non Transactional State Store - 500 ERR_STATE_STORE_NOT_SUPPORTED", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", nonTransactionalStoreName)
		testTransactionalOperations := []stateTransactionRequestBodyOperation{
			{
				Operation: string(state.OperationUpsert),
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
		})

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode, "Dapr should return 500")
		assert.Equal(t, "ERR_STATE_STORE_NOT_SUPPORTED", resp.ErrorBody["errorCode"], apiPath)
	})

	t.Run("Direct Transaction upstream failure - 500 ERR_STATE_TRANSACTION", func(t *testing.T) {
		apiPath := fmt.Sprintf("v1.0/state/%s/transaction", storeName)
		testTransactionalOperations := []stateTransactionRequestBodyOperation{
			{
				Operation: string(state.OperationUpsert),
				Request: map[string]interface{}{
					"key":   "fakeKey1",
					"value": fakeBodyObject,
				},
			},
			{
				Operation: string(state.OperationDelete),
				Request: map[string]interface{}{
					"key": "fakeKey1",
				},
			},
		}

		// act
		inputBodyBytes, err := json.Marshal(stateTransactionRequestBody{
			Operations: testTransactionalOperations,
			Metadata: map[string]string{
				"error": "true",
			},
		})

		require.NoError(t, err)
		resp := fakeServer.DoRequest("POST", apiPath, inputBodyBytes, nil)

		// assert
		assert.Equal(t, 500, resp.StatusCode, "Dapr should return 500")
		assert.Equal(t, "ERR_STATE_TRANSACTION", resp.ErrorBody["errorCode"], apiPath)
	})
	fakeServer.Shutdown()
}

func TestStateStoreErrors(t *testing.T) {
	t.Run("non etag error", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 500, c)
		assert.Equal(t, "error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag mismatch error", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 409, c)
		assert.Equal(t, "possible etag mismatch. error from state store: error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag invalid error", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		c, m, r := a.stateErrorResponse(err, "ERR_STATE_SAVE")

		assert.Equal(t, 400, c)
		assert.Equal(t, "invalid etag value: error", m)
		assert.Equal(t, "ERR_STATE_SAVE", r.ErrorCode)
	})

	t.Run("etag error mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		e, c, m := a.etagError(err)

		assert.True(t, e)
		assert.Equal(t, 409, c)
		assert.Equal(t, "possible etag mismatch. error from state store: error", m)
	})

	t.Run("etag error invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		e, c, m := a.etagError(err)

		assert.True(t, e)
		assert.Equal(t, 400, c)
		assert.Equal(t, "invalid etag value: error", m)
	})
}

func TestExtractEtag(t *testing.T) {
	t.Run("no etag present", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}

		ok, etag := extractEtag(&r)
		assert.False(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("empty etag exists", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Add("If-Match", "")

		ok, etag := extractEtag(&r)
		assert.True(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("non-empty etag exists", func(t *testing.T) {
		r := fasthttp.RequestCtx{
			Request: fasthttp.Request{},
		}
		r.Request.Header.Add("If-Match", "a")

		ok, etag := extractEtag(&r)
		assert.True(t, ok)
		assert.Equal(t, "a", etag)
	})
}

func matchContextInterface(v any) bool {
	_, ok := v.(context.Context)
	return ok
}
