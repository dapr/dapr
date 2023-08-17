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

package stateapp_e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName              = "stateapp-errorcodes"                       // App name in Dapr.
	appNamePluggable     = "stateapp-pluggable-errorcodes"             // App name with pluggable components in Dapr.
	redisPluggableApp    = "e2e-pluggable_redis-statestore-errorcodes" // The name of the pluggable component app.
	numHealthChecks      = 60                                          // Number of get calls before starting tests.
	testManyEntriesCount = 5                                           // Anything between 1 and the number above (inclusive).
)

type testCommandRequest struct {
	RemoteApp string `json:"remoteApp,omitempty"`
	Method    string `json:"method,omitempty"`
}

// appState represents a state in this app.
type appState struct {
	Data []byte `json:"data,omitempty"`
}

// daprState represents a state in Dapr.
type daprState struct {
	Key   string    `json:"key,omitempty"`
	Value *appState `json:"value,omitempty"`
}

// requestResponse represents a request or response for the APIs in the app.
type requestResponse struct {
	States []daprState `json:"states,omitempty"`
}

// represents each step in a test since it can make multiple calls.
type testStep struct {
	command            string
	request            requestResponse
	expectedResponse   requestResponse
	expectedStatusCode int
}

// stateTransactionRequest represents a request for state transactions
type stateTransaction struct {
	Key           string    `json:"key,omitempty"`
	Value         *appState `json:"value,omitempty"`
	OperationType string    `json:"operationType,omitempty"`
}

// represents each step in a test for state transactions
type stateTransactionTestStep struct {
	command          string
	request          stateTransactionRequestResponse
	expectedResponse requestResponse
}

type stateTransactionRequestResponse struct {
	States []stateTransaction
}

type testCase struct {
	name     string
	steps    []testStep
	protocol string
}

type testStateTransactionCase struct {
	steps []stateTransactionTestStep
}

func generateDaprState(kv utils.SimpleKeyValue) daprState {
	if kv.Key == nil {
		return daprState{}
	}

	key := fmt.Sprintf("%v", kv.Key)
	if kv.Value == nil {
		return daprState{key, nil}
	}

	value := []byte(fmt.Sprintf("%v", kv.Value))
	return daprState{key, &appState{value}}
}

// creates a requestResponse based on an array of key value pairs.
func newRequestResponse(keyValues ...utils.SimpleKeyValue) requestResponse {
	daprStates := make([]daprState, 0, len(keyValues))
	for _, keyValue := range keyValues {
		daprStates = append(daprStates, generateDaprState(keyValue))
	}

	return requestResponse{
		daprStates,
	}
}

// creates a requestResponse based on an array of key value pairs.
func newStateTransactionRequestResponse(keyValues ...utils.StateTransactionKeyValue) stateTransactionRequestResponse {
	daprStateTransactions := make([]stateTransaction, 0, len(keyValues))
	for _, keyValue := range keyValues {
		daprStateTransactions = append(daprStateTransactions, stateTransaction{
			keyValue.Key, &appState{[]byte(keyValue.Value)}, keyValue.OperationType,
		})
	}

	return stateTransactionRequestResponse{
		daprStateTransactions,
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

func generateTestCases(isHTTP bool) []testCase {
	protocol := "grpc"

	if isHTTP {
		protocol = "http"
	}

	// Just for readability
	emptyRequest := requestResponse{}
	emptyResponse := requestResponse{}

	testCase1Key := guuid.New().String()
	testCase1Value := "The best song ever is 'Highwayman' by 'The Highwaymen'."

	testCaseManyKeys := utils.GenerateRandomStringKeys(testManyEntriesCount)
	testCaseManyKeyValues := utils.GenerateRandomStringValues(testCaseManyKeys)

	return []testCase{
		{
			// No comma since this will become the name of the test without spaces.
			"Test get save delete with empty request response for single app and single hop " + protocol,
			[]testStep{
				{
					"get",
					emptyRequest,
					emptyResponse,
					200,
				},
				{
					"save",
					emptyRequest,
					emptyResponse,
					204,
				},
				{
					"delete",
					emptyRequest,
					emptyResponse,
					204,
				},
			},
			protocol,
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test save get and delete a single item for single app and single hop " + protocol,
			[]testStep{
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, nil}),
					200,
				},
				{
					"save",
					newRequest(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
					emptyResponse,
					204,
				},
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
					200,
				},
				{
					"delete",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					emptyResponse,
					204,
				},
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, nil}),
					200,
				},
			},
			protocol,
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test save get getbulk and delete on multiple items for single app and single hop " + protocol,
			[]testStep{
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeys...),
					200,
				},
				{
					"save",
					newRequest(testCaseManyKeyValues...),
					emptyResponse,
					204,
				},
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeyValues...),
					200,
				},
				{
					"getbulk",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeyValues...),
					200,
				},
				{
					"delete",
					newRequest(testCaseManyKeys...),
					emptyResponse,
					204,
				},
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeys...),
					200,
				},
			},
			protocol,
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test data size edges (1MB < limit < 4MB) " + protocol,
			[]testStep{
				{
					"save",
					generateSpecificLengthSample(1024 * 1024), // Less than limit
					emptyResponse,
					204,
				},
				{
					"save",
					generateSpecificLengthSample(1024*1024*4 + 1), // Limit + 1
					emptyResponse,
					500,
				},
				{
					"save",
					generateSpecificLengthSample(1024 * 1024 * 8), // Greater than limit
					emptyResponse,
					500,
				},
			},
			protocol,
		},
	}
}

func generateStateTransactionCases(protocolType string) testStateTransactionCase {
	testCase1Key, testCase2Key := guuid.New().String()+protocolType, guuid.New().String()+protocolType
	testCase1Value := "The best song ever is 'Highwayman' by 'The Highwaymen'."
	testCase2Value := "Hello World"

	// Just for readability
	emptyResponse := requestResponse{}

	testStateTransactionCase := testStateTransactionCase{
		[]stateTransactionTestStep{
			{
				// Transaction order should be tested in conformance tests: https://github.com/dapr/components-contrib/issues/1210
				"transact",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, testCase1Value, "upsert"},
				),
				emptyResponse,
			},
			{
				"transact",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, "", "delete"},
					utils.StateTransactionKeyValue{testCase2Key, testCase2Value, "upsert"},
				),
				emptyResponse,
			},
			{
				"get",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, "", ""},
				),
				newResponse(utils.SimpleKeyValue{testCase1Key, nil}),
			},
			{
				"get",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase2Key, "", ""},
				),
				newResponse(utils.SimpleKeyValue{testCase2Key, testCase2Value}),
			},
			{
				"transact",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, testCase1Value, "upsert"},
					utils.StateTransactionKeyValue{testCase1Key, testCase2Value, "upsert"},
				),
				emptyResponse,
			},
			{
				"get",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, "", ""},
				),
				newResponse(utils.SimpleKeyValue{testCase1Key, testCase2Value}),
			},
		},
	}
	return testStateTransactionCase
}

func generateSpecificLengthSample(sizeInBytes int) requestResponse {
	key := guuid.New().String()
	val := make([]byte, sizeInBytes)

	state := []daprState{
		{
			key,
			&appState{val},
		},
	}

	return requestResponse{
		state,
	}
}

var (
	tr             *runner.TestRunner
	stateStoreApps []struct {
		name       string
		stateStore string
	} = []struct {
		name       string
		stateStore string
	}{
		{
			name:       appName,
			stateStore: "statestore",
		},
	}
)

func TestMain(m *testing.M) {
	utils.SetupLogs("stateapp-errorcodes")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-stateapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			Config:         "errorcodes",
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestEtags(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	testCases := []struct {
		protocol string
	}{
		{protocol: "http"},
		{protocol: "grpc"},
	}

	// Now we are ready to run the actual tests
	for _, tt := range testCases {
		t.Run(fmt.Sprintf("Test Etags ErrorCodes using %s protocol", tt.protocol), func(t *testing.T) {
			url := strings.TrimSpace(fmt.Sprintf("%s/test-etag-errorcodes/%s/statestore", externalURL, tt.protocol))
			resp, status, err := utils.HTTPPostWithStatus(url, nil)
			require.NoError(t, err)

			// The test passes with 204 if there's no error
			assert.Equalf(t, http.StatusNoContent, status, "Test failed. Body is: %q", string(resp))
		})
	}
}
