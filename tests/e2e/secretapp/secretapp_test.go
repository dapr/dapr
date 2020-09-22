// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretapp_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const appName = "secretapp" // App name in Dapr.
const numHealthChecks = 60  // Number of get calls before starting tests.

// daprSecret represents a secret in Dapr.
type daprSecret struct {
	Key   string             `json:"key,omitempty"`
	Value *map[string]string `json:"value,omitempty"`
}

// requestResponse represents a request or response for the APIs in the app.
type requestResponse struct {
	Secrets []daprSecret `json:"secrets,omitempty"`
}

// represents each step in a test since it can make multiple calls.
type testStep struct {
	command          string
	request          requestResponse
	expectedResponse requestResponse
	errorExpected    bool
	statusCode       int
}

type testCase struct {
	name  string
	steps []testStep
}

func generateDaprSecret(kv utils.SimpleKeyValue) daprSecret {
	if kv.Key == nil {
		return daprSecret{}
	}

	key := fmt.Sprintf("%v", kv.Key)
	if kv.Value == nil {
		return daprSecret{key, nil}
	}

	secret := fmt.Sprintf("%v", kv.Value)
	value := map[string]string{"username": secret}
	return daprSecret{key, &value}
}

// creates a requestResponse based on an array of key value pairs.
func newRequestResponse(keyValues ...utils.SimpleKeyValue) requestResponse {
	daprSecrets := make([]daprSecret, 0, len(keyValues))
	for _, keyValue := range keyValues {
		daprSecrets = append(daprSecrets, generateDaprSecret(keyValue))
	}

	return requestResponse{
		daprSecrets,
	}
}

// Just for readability.
func newRequest(keyValues ...utils.SimpleKeyValue) requestResponse {
	return newRequestResponse(keyValues...)
}

// Just for readability.
func newResponse(keyValues ...utils.SimpleKeyValue) requestResponse {
	return newRequestResponse(keyValues...)
}

func generateTestCases() []testCase {
	// Just for readability
	emptyRequest := requestResponse{
		nil,
	}

	// Just for readability
	emptyResponse := requestResponse{
		nil,
	}

	testCase1Key := "daprsecret"
	testCase2Key := "daprsecret2"
	testCase1Value := "admin"

	return []testCase{
		{
			// No comma since this will become the name of the test without spaces.
			"Test get with empty request response for single app and single hop",
			[]testStep{
				{
					"get",
					emptyRequest,
					emptyResponse,
					false,
					200,
				},
			},
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test get a single item for single app and single hop",
			[]testStep{
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
					newResponse(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
					false,
					200,
				},
			},
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test unallowed secret",
			[]testStep{
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase2Key, ""}),
					newResponse(utils.SimpleKeyValue{testCase2Key, ""}),
					true,
					403,
				},
			},
		},
	}
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			Config:         "secretappconfig",
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-secretapp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestSecretApp(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	testCases := generateTestCases()

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Now we are ready to run the actual tests
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			for _, step := range tt.steps {
				body, err := json.Marshal(step.request)
				require.NoError(t, err)

				url := fmt.Sprintf("%s/test/%s", externalURL, step.command)

				resp, statusCode, err := utils.HTTPPostWithStatus(url, body)
				if !step.errorExpected {
					require.NoError(t, err)

					var appResp requestResponse
					err = json.Unmarshal(resp, &appResp)
					require.NoError(t, err)
					require.True(t, reflect.DeepEqual(step.expectedResponse, appResp))
				} else {
					require.Equal(t, step.statusCode, statusCode, "Expected statusCode to be equal")
				}
			}
		})
	}
}
