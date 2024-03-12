/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process/exec"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	componentspubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	kiterrors "github.com/dapr/kit/errors"
)

const (
	ErrInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	ResourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
)

func init() {
	suite.Register(new(standardizedErrors))
}

type standardizedErrors struct {
	daprd *daprd.Daprd
}

func (e *standardizedErrors) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	// Darwin enforces a maximum 104 byte socket name limit, so we need to be a
	// bit fancy on how we generate the name.
	tmp, err := nettest.LocalPath()
	require.NoError(t, err)

	socketDir := filepath.Join(tmp, util.RandomString(t, 4))
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketDir))
	})

	pubsubInMem := pubsub.New(t,
		pubsub.WithSocketDirectory(socketDir),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t,
			inmemory.WithFeatures(),
			inmemory.WithPublishFn(func(ctx context.Context, req *componentspubsub.PublishRequest) error {
				return errors.New("outbox error")
			}),
		)),
	)

	// spin up a new daprd with a pubsub component
	e.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypubsub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub-outbox
spec:
  type: pubsub.%s
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: state-outbox
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: outboxPublishPubsub
    value: "pubsub-outbox"
  - name: outboxPublishTopic
    value: "goodTopic" # force topic mismatch error
  - name: outboxPubsub
    value: "pubsub-outbox"
  - name: outboxDiscardWhenMissingState #Optional. Defaults to false
    value: false
`, pubsubInMem.SocketName())),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_COMPONENTS_SOCKETS_FOLDER", socketDir,
		)),
	)

	return []framework.Option{
		framework.WithProcesses(pubsubInMem, e.daprd),
	}
}

func (e *standardizedErrors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	// Covers apierrors.PubSubNotFound()
	t.Run("pubsub doesn't exist", func(t *testing.T) {
		name := "pubsub-doesn't-exist"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/topic", e.daprd.HTTPPort(), name)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(""))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBSUB_NOT_FOUND", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, fmt.Sprintf("pubsub %s is not found", name), errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+kiterrors.CodeNotFound, detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	t.Run("pubsub unmarshal events", func(t *testing.T) {
		name := "mypubsub"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/publish/bulk/%s/topic", e.daprd.HTTPPort(), name)
		payload := `{"entryID": "}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBSUB_EVENTS_SER", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		expectedErr := "unexpected end of JSON input"
		require.Equal(t, fmt.Sprintf("error when unmarshaling the request for topic topic pubsub %s: %s", name, expectedErr), errMsg) //nolint:dupword

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+"UNMARSHAL_EVENTS", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "pubsub", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.PubSubMetadataDeserialize()
	t.Run("pubsub metadata deserialization", func(t *testing.T) {
		name := "mypubsub"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/topic?metadata.rawPayload=invalidBooleanValue", e.daprd.HTTPPort(), name)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(""))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBSUB_REQUEST_METADATA", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		expectedErr := "rawPayload value must be a valid boolean: actual is 'invalidBooleanValue'"
		require.Equal(t, fmt.Sprintf("failed deserializing metadata. Error: %s", expectedErr), errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+"METADATA_DESERIALIZATION", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "pubsub", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.PubSubCloudEventCreation()
	t.Run("pubsub cloud event creation issue", func(t *testing.T) {
		payload := `{"}`
		name := "mypubsub"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/topic?metadata.rawPayload=false", e.daprd.HTTPPort(), name)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/cloudevents+json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBSUB_CLOUD_EVENTS_SER", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "cannot create cloudevent", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+"CLOUD_EVENT_CREATION", errInfo["reason"])
		require.NotNil(t, errInfo["metadata"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "pubsub", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	// Covers apierrors.PubSubMarshalEvents()
	t.Run("pubsub marshal events issue", func(t *testing.T) {
		name := "mypubsub"
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/publish/bulk/%s/topic", e.daprd.HTTPPort(), name)
		payload := `{"entryID": ""}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBSUB_EVENTS_SER", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		expectedErr := "json: cannot unmarshal object into Go value of type []http.bulkPublishMessageEntry"
		require.Equal(t, fmt.Sprintf("error when unmarshaling the request for topic topic pubsub %s: %s", name, expectedErr), errMsg) //nolint:dupword

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Parse the json into go objects
		var errInfo map[string]interface{}
		var resInfo map[string]interface{}

		for _, detail := range detailsArray {
			d, ok := detail.(map[string]interface{})
			require.True(t, ok)
			switch d["@type"] {
			case ErrInfoType:
				errInfo = d
			case ResourceInfoType:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		// Confirm that the ErrorInfo details are correct
		require.NotEmptyf(t, errInfo, "ErrorInfo not found in %+v", detailsArray)
		require.Equal(t, "dapr.io", errInfo["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+"UNMARSHAL_EVENTS", errInfo["reason"])

		// Confirm that the ResourceInfo details are correct
		require.NotEmptyf(t, resInfo, "ResourceInfo not found in %+v", detailsArray)
		require.Equal(t, "pubsub", resInfo["resource_type"])
		require.Equal(t, name, resInfo["resource_name"])
	})

	t.Run("pubsub outbox", func(t *testing.T) {
		name := "state-outbox"
		payload := `{"operations": [{"operation": "upsert","request": {"key": "key1","value": "val1"}},{"operation": "delete","request": {"key": "key2"}}],"metadata": {"partitionKey": "key2"}}`
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/transaction", e.daprd.HTTPPort(), name)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_PUBLISH_OUTBOX", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		expectedErrMsg := "error while publishing outbox message: rpc error: code = Unknown desc = outbox error"
		require.Equal(t, expectedErrMsg, errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		require.Equal(t, kiterrors.CodePrefixPubSub+"OUTBOX", detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})
}
