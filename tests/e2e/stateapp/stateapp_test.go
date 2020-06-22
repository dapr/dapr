// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package stateapp_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const appName = "stateapp"     // App name in Dapr.
const numHealthChecks = 60     // Number of get calls before starting tests.
const testManyEntriesCount = 5 // Anything between 1 and the number above (inclusive).

type testCommandRequest struct {
	RemoteApp string `json:"remoteApp,omitempty"`
	Method    string `json:"method,omitempty"`
}

// appState represents a state in this app.
type appState struct {
	Data string `json:"data,omitempty"`
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
	command          string
	request          requestResponse
	expectedResponse requestResponse
}

//  stateTransactionRequest represents a request for state transactions
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
	name  string
	steps []testStep
}

type testStateTransactionCase struct {
	name  string
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

	value := fmt.Sprintf("%v", kv.Value)
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
			keyValue.Key, &appState{keyValue.Value}, keyValue.OperationType,
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

func generateTestCases() []testCase {
	// Just for readability
	emptyRequest := requestResponse{
		nil,
	}

	// Just for readability
	emptyResponse := requestResponse{
		nil,
	}

	testCase1Key := guuid.New().String()
	testCase1Value := "The best song ever is 'Highwayman' by 'The Highwaymen'."

	testCaseManyKeys := utils.GenerateRandomStringKeys(testManyEntriesCount)
	testCaseManyKeyValues := utils.GenerateRandomStringValues(testCaseManyKeys)

	return []testCase{
		{
			// No comma since this will become the name of the test without spaces.
			"Test get save delete with empty request response for single app and single hop",
			[]testStep{
				{
					"get",
					emptyRequest,
					emptyResponse,
				},
				{
					"save",
					emptyRequest,
					emptyResponse,
				},
				{
					"delete",
					emptyRequest,
					emptyResponse,
				},
			},
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test save get and delete a single item for single app and single hop",
			[]testStep{
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, nil}),
				},
				{
					"save",
					newRequest(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
					emptyResponse,
				},
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, testCase1Value}),
				},
				{
					"delete",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					emptyResponse,
				},
				{
					"get",
					newRequest(utils.SimpleKeyValue{testCase1Key, nil}),
					newResponse(utils.SimpleKeyValue{testCase1Key, nil}),
				},
			},
		},
		{
			// No comma since this will become the name of the test without spaces.
			"Test save get and delete on multiple items for single app and single hop",
			[]testStep{
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeys...),
				},
				{
					"save",
					newRequest(testCaseManyKeyValues...),
					emptyResponse,
				},
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeyValues...),
				},
				{
					"delete",
					newRequest(testCaseManyKeys...),
					emptyResponse,
				},
				{
					"get",
					newRequest(testCaseManyKeys...),
					newResponse(testCaseManyKeys...),
				},
			},
		},
	}
}

func generateStateTransactionCases() testStateTransactionCase {
	testCase1Key, testCase2Key := guuid.New().String(), guuid.New().String()
	testCase1Value := "The best song ever is 'Highwayman' by 'The Highwaymen'."
	testCase2Value := "Hello World"
	// Just for readability
	emptyResponse := requestResponse{
		nil,
	}

	testStateTransactionCase := testStateTransactionCase{
		"Test state transaction APIs with multiple transactions",
		[]stateTransactionTestStep{
			{
				"transact",
				newStateTransactionRequestResponse(
					utils.StateTransactionKeyValue{testCase1Key, testCase1Value, "upsert"},
					utils.StateTransactionKeyValue{testCase1Key, "", "delete"},
					utils.StateTransactionKeyValue{testCase2Key, testCase2Value, "upsert"},
					utils.StateTransactionKeyValue{testCase2Key, "", "delete"},
				),
				emptyResponse,
			},
		},
	}
	return testStateTransactionCase
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-stateapp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestStateApp(t *testing.T) {
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

				resp, err := utils.HTTPPost(url, body)
				require.NoError(t, err)

				var appResp requestResponse
				err = json.Unmarshal(resp, &appResp)
				require.NoError(t, err)
				require.True(t, reflect.DeepEqual(step.expectedResponse, appResp))
			}
		})
	}
}

func TestStateTransactionApps(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	testStateTransactionCase := generateStateTransactionCases()
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Now we are ready to run the actual tests
	t.Run(testStateTransactionCase.name, func(t *testing.T) {
		for _, step := range testStateTransactionCase.steps {
			body, err := json.Marshal(step.request)
			require.NoError(t, err)

			url := fmt.Sprintf("%s/test/%s", externalURL, step.command)

			resp, err := utils.HTTPPost(url, body)
			require.NoError(t, err)

			var appResp requestResponse
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.True(t, reflect.DeepEqual(step.expectedResponse, appResp))
		}
	})
}
