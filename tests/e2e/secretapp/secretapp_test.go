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

package secretapp_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	// Although Kubernetes secret store components are disabled, this is the built-in one that will work anyways
	secretStore       = "kubernetes"
	badSecretStore    = "vault"
	nonexistentStore  = "nonexistent"
	appName           = "secretapp" // App name in Dapr.
	numHealthChecks   = 60          // Number of get calls before starting tests.
	allowedSecret     = "daprsecret"
	unallowedSecret   = "daprsecret2"
	nonExistentSecret = "nonexistentsecret"
	emptySecret       = "emptysecret"
	testCase1Value    = "admin"
)

// daprSecret represents a secret in Dapr.
type daprSecret struct {
	Key   string             `json:"key,omitempty"`
	Value *map[string]string `json:"value,omitempty"`
	Store string             `json:"store,omitempty"`
}

// requestResponse represents a request or response for the APIs in the app.
type requestResponse struct {
	Secrets []daprSecret `json:"secrets,omitempty"`
}

// represents each step in a test since it can make multiple calls.
type testCase struct {
	name             string
	request          requestResponse
	expectedResponse requestResponse
	errorExpected    bool
	statusCode       int
	errorString      string
}

func generateDaprSecret(kv utils.SimpleKeyValue, store string) daprSecret {
	if kv.Key == nil {
		return daprSecret{}
	}

	key := fmt.Sprintf("%v", kv.Key)
	if kv.Value == nil {
		return daprSecret{key, nil, ""}
	}

	secret := fmt.Sprintf("%v", kv.Value)
	value := map[string]string{}
	if secret != "" {
		value["username"] = secret
	}
	return daprSecret{key, &value, store}
}

// creates a requestResponse based on an array of key value pairs.
func newRequestResponse(store string, keyValues ...utils.SimpleKeyValue) requestResponse {
	daprSecrets := make([]daprSecret, 0, len(keyValues))
	for _, keyValue := range keyValues {
		daprSecrets = append(daprSecrets, generateDaprSecret(keyValue, store))
	}

	return requestResponse{
		daprSecrets,
	}
}

// Just for readability.
func newRequest(store string, keyValues ...utils.SimpleKeyValue) requestResponse {
	return newRequestResponse(store, keyValues...)
}

// Just for readability.
func newResponse(store string, keyValues ...utils.SimpleKeyValue) requestResponse {
	return newRequestResponse(store, keyValues...)
}

func generateTestCases() []testCase {
	// Just for readability
	emptyRequest := requestResponse{}

	// Just for readability
	emptyResponse := requestResponse{}

	return []testCase{
		{
			// No comma since this will become the name of the test without spaces.
			"empty request",
			emptyRequest,
			emptyResponse,
			false,
			200,
			"", // no error
		},
		{
			"empty secret",
			newRequest(secretStore, utils.SimpleKeyValue{emptySecret, ""}),
			newResponse(secretStore, utils.SimpleKeyValue{emptySecret, ""}),
			false,
			200,
			"", // no error
		},
		{
			"allowed secret",
			newRequest(secretStore, utils.SimpleKeyValue{allowedSecret, testCase1Value}),
			newResponse(secretStore, utils.SimpleKeyValue{allowedSecret, testCase1Value}),
			false,
			200,
			"", // no error
		},
		{
			"unallowed secret",
			newRequest(secretStore, utils.SimpleKeyValue{unallowedSecret, ""}),
			newResponse("", utils.SimpleKeyValue{unallowedSecret, ""}),
			true,
			403,
			"ERR_PERMISSION_DENIED",
		},
		{
			"nonexistent secret",
			newRequest(secretStore, utils.SimpleKeyValue{nonExistentSecret, ""}),
			newResponse("", utils.SimpleKeyValue{nonExistentSecret, ""}),
			true,
			500,
			"ERR_SECRET_GET",
		},
		{
			"secret from nonexistent secret store",
			newRequest(nonexistentStore, utils.SimpleKeyValue{allowedSecret, ""}),
			newResponse("", utils.SimpleKeyValue{allowedSecret, ""}),
			true,
			401,
			"ERR_SECRET_STORE_NOT_FOUND",
		},
		{
			"secret from the disabled secret store",
			newRequest(badSecretStore, utils.SimpleKeyValue{allowedSecret, ""}),
			newResponse("", utils.SimpleKeyValue{allowedSecret, ""}),
			true,
			401,
			"ERR_SECRET_STORE_NOT_FOUND",
		},
	}
}

func generateTestCasesForDisabledSecretStore() []testCase {
	return []testCase{
		{
			"valid secret but secret store should not be found",
			newRequest(secretStore, utils.SimpleKeyValue{allowedSecret, testCase1Value}),
			newResponse(secretStore, utils.SimpleKeyValue{allowedSecret, testCase1Value}),
			true,
			500,
			"ERR_SECRET_STORES_NOT_CONFIGURED",
		},
	}
}

var secretAppTests = []struct {
	app       string
	testCases []testCase
}{
	{
		"secretapp",
		generateTestCases(),
	},
	{
		"secretapp-disable",
		generateTestCasesForDisabledSecretStore(),
	},
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("secretapp")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			// "secretappconfig" restricts access to the Kubernetes secret store, but the built-in one (called "kubernetes") should work anyways
			Config:         "secretappconfig",
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-secretapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			Config:             "secretappconfig",
			AppName:            "secretapp-disable",
			DaprEnabled:        true,
			ImageName:          "e2e-secretapp",
			Replicas:           1,
			IngressEnabled:     true,
			MetricsEnabled:     true,
			SecretStoreDisable: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestSecretApp(t *testing.T) {
	for _, tt := range secretAppTests {
		externalURL := tr.Platform.AcquireAppExternalURL(tt.app)
		require.NotEmpty(t, externalURL, "external URL must not be empty!")
		testCases := tt.testCases

		// This initial probe makes the test wait a little bit longer when needed,
		// making this test less flaky due to delays in the deployment.
		_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
		require.NoError(t, err)

		// Now we are ready to run the actual tests
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// setup
				body, err := json.Marshal(tc.request)
				require.NoError(t, err)
				url := fmt.Sprintf("%s/test/get", externalURL)

				// act
				resp, statusCode, err := utils.HTTPPostWithStatus(url, body)

				// assert
				if !tc.errorExpected {
					require.NoError(t, err)

					var appResp requestResponse
					err = json.Unmarshal(resp, &appResp)
					require.NoError(t, err)

					require.True(t, reflect.DeepEqual(tc.expectedResponse, appResp))
					require.Equal(t, tc.statusCode, statusCode, "Expected statusCode to be equal")
				} else {
					require.Contains(t, string(resp), tc.errorString, "Expected error string to match")
					require.Equal(t, tc.statusCode, statusCode, "Expected statusCode to be equal")
				}
			})
		}
	}
}
