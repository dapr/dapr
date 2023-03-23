//go:build e2e
// +build e2e

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
package activation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName         = "actorstate" // App name in Dapr.
	numHealthChecks = 60           // Number of get calls before starting tests.
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_state")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test and will be
	// cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-actorstate",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorState(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	httpURL := path.Join("%s/test/actor_state_http", externalURL)
	grpcURL := path.Join("%s/test/actor_state_grpc", externalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Wait until runtime finds the leader of placements.
	time.Sleep(15 * time.Second)

	t.Run("http", func(t *testing.T) {
		t.Run("getting state which does not exist should error", func(t *testing.T) {
			resp, code, err := utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID/doesnotexist", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, code)
			t.Logf("response: %s", string(resp))
		})

		t.Run("should be able to save, get, update and delete state", func(t *testing.T) {
			resp, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID", httpURL), []byte(`{key:"myKey",value:"myData"}`))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			t.Logf("response: %s", string(resp))

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, "myData", string(resp))

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/nothttpMyActorType/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, code)
			t.Logf("response: %s", string(resp))

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/notmyActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, code)
			t.Logf("response: %s", string(resp))

			resp, code, err = utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID", httpURL), []byte(`{key:"myKey",value:"newData"}`))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.NotEmpty(t, resp)

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, "newData", string(resp))

			resp, code, err = utils.HTTPDeleteWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Empty(t, resp)

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorType/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, code)
			t.Logf("response: %s", string(resp))
		})

		t.Run("data saved with TTL should be automatically deleted", func(t *testing.T) {
			resp, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/httpMyActorTypeTTL/myActorID", httpURL), []byte(`{key:"myKey",value:"myData","metadata":{"ttlInSeconds":5}}`))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Empty(t, resp)

			resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorTypeTTL/myActorID/myKey", httpURL))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.Empty(t, "myData", string(resp))

			assert.Eventually(t, func() bool {
				resp, code, err = utils.HTTPGetWithStatus(fmt.Sprintf("%s/httpMyActorTypeTTL/myActorID/myKey", httpURL))
				return err == nil && code == http.StatusNotFound
			}, 10*time.Second, 1*time.Second, "state should be deleted after TTL: %s", code)
		})
	})

	t.Run("grpc", func(t *testing.T) {
		t.Run("getting state which does not exist should error", func(t *testing.T) {
			b, err := json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "doesnotexist",
			})
			require.NoError(t, err)

			_, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.Error(t, err)
			assert.Equal(t, http.StatusInternalServerError, code)
		})

		t.Run("should be able to save, get, update and delete state", func(t *testing.T) {
			b, err := json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID",
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "upsert", Key: "myKey",
						Value: &anypb.Any{Value: []byte("myData")}},
				},
			})
			require.NoError(t, err)
			_, code, err := utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			var gresp runtimev1.GetActorStateResponse
			assert.NoError(t, json.Unmarshal(resp, &gresp))
			assert.Equal(t, []byte("myData"), gresp.Data)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "notgrpcMyActorType", ActorId: "myActorID", Key: "myKey",
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "notmyActorID", Key: "myKey",
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, code)

			b, err = json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID",
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "upsert", Key: "myKey",
						Value: &anypb.Any{Value: []byte("newData")}},
				},
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			assert.NoError(t, json.Unmarshal(resp, &gresp))
			assert.Equal(t, []byte("newData"), gresp.Data)

			b, err = json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID",
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{OperationType: "delete", Key: "myKey"},
				},
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "myKey",
			})
			require.NoError(t, err)
			_, code, err = utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, code)
		})

		t.Run("data saved with TTL should be automatically deleted", func(t *testing.T) {
			b, err := json.Marshal(&runtimev1.ExecuteActorStateTransactionRequest{
				ActorType: "grpcMyActorTypeTTL", ActorId: "myActorID",
				Operations: []*runtimev1.TransactionalActorStateOperation{
					{
						OperationType: "upsert", Key: "myKey",
						Value:    &anypb.Any{Value: []byte("myData")},
						Metadata: map[string]string{"ttlInSeconds": "3"},
					},
				},
			})
			require.NoError(t, err)
			_, code, err := utils.HTTPPostWithStatus(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)

			b, err = json.Marshal(&runtimev1.GetActorStateRequest{
				ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "myKey",
			})
			require.NoError(t, err)
			resp, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, code)
			var gresp runtimev1.GetActorStateResponse
			assert.NoError(t, json.Unmarshal(resp, &gresp))
			assert.Equal(t, []byte("newData"), gresp.Data)

			assert.Eventually(t, func() bool {
				b, err = json.Marshal(&runtimev1.GetActorStateRequest{
					ActorType: "grpcMyActorType", ActorId: "myActorID", Key: "myKey",
				})
				require.NoError(t, err)
				_, code, err := utils.HTTPGetWithStatusWithData(grpcURL, b)
				return err != nil && code == http.StatusInternalServerError
			}, 10*time.Second, 1*time.Second)
		})
	})
}
