//go:build e2e
// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package stateapp_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	appName              = "stateapp" // App name in Dapr.
	numHealthChecks      = 60         // Number of get calls before starting tests.
	testManyEntriesCount = 5          // Anything between 1 and the number above (inclusive).
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
	emptyResponse := requestResponse{
		nil,
	}

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
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestStateApp(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	testCases := generateTestCases(true)                       // For HTTP
	testCases = append(testCases, generateTestCases(false)...) // For gRPC

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Now we are ready to run the actual tests
	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, step := range tt.steps {
				body, err := json.Marshal(step.request)
				require.NoError(t, err)

				url := fmt.Sprintf("%s/test/%s/%s/statestore", externalURL, tt.protocol, step.command)

				resp, statusCode, err := utils.HTTPPostWithStatus(url, body)
				require.NoError(t, err)
				require.Equal(t, step.expectedStatusCode, statusCode, url)

				var appResp requestResponse
				if statusCode != 204 {
					err = json.Unmarshal(resp, &appResp)

					require.NoError(t, err)
				}

				for _, er := range step.expectedResponse.States {
					for _, ri := range appResp.States {
						if er.Key == ri.Key {
							require.True(t, reflect.DeepEqual(er.Key, ri.Key))

							if er.Value != nil {
								require.True(t, reflect.DeepEqual(er.Value.Data, ri.Value.Data))
							}
						}
					}
				}
			}
		})
	}
}

func TestStateTransactionApps(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	var transactionTests = []struct {
		protocol string
		in       testStateTransactionCase
	}{
		{"HTTP", generateStateTransactionCases("HTTP")},
		{"GRPC", generateStateTransactionCases("GRPC")},
	}

	// Now we are ready to run the actual tests
	for _, tt := range transactionTests {
		t.Run(fmt.Sprintf("Test State Transactions using %s protocol", tt.protocol), func(t *testing.T) {
			for _, step := range tt.in.steps {
				body, err := json.Marshal(step.request)
				require.NoError(t, err)
				var url string
				if tt.protocol == "HTTP" || step.command == "get" {
					url = strings.TrimSpace(fmt.Sprintf("%s/test/http/%s/statestore", externalURL, step.command))
				} else {
					url = strings.TrimSpace(fmt.Sprintf("%s/test/grpc/%s/statestore", externalURL, step.command))
				}
				resp, err := utils.HTTPPost(url, body)
				require.NoError(t, err)

				var appResp requestResponse
				err = json.Unmarshal(resp, &appResp)

				require.NoError(t, err)
				require.Equal(t, len(step.expectedResponse.States), len(appResp.States))

				for _, er := range step.expectedResponse.States {
					for _, ri := range appResp.States {
						if er.Key == ri.Key {
							require.Equal(t, er.Key, ri.Key)
							require.Equal(t, er.Value != nil, ri.Value != nil)
							if er.Value != nil {
								require.True(t, reflect.DeepEqual(er.Value.Data, ri.Value.Data))
							}
						}
					}
				}
			}
		})
	}
}

func TestMissingKeyDirect(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, protocol := range []string{"http", "grpc"} {
		t.Run(fmt.Sprintf("missing_key_%s", protocol), func(t *testing.T) {
			stateURL := fmt.Sprintf("%s/test/%s/%s/statestore", externalURL, protocol, "get")

			key := guuid.New().String()
			request := newRequest(utils.SimpleKeyValue{key, nil})
			body, _ := json.Marshal(request)

			resp, status, err := utils.HTTPPostWithStatus(stateURL, body)

			var states requestResponse
			json.Unmarshal(resp, &states)

			require.Equal(t, 200, status)
			require.Nil(t, err)
			require.Len(t, states.States, 1)
			require.Equal(t, states.States[0].Key, key)
			require.Nil(t, states.States[0].Value)
		})
	}
}

func TestMissingAndMisconfiguredStateStore(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	testCases := []struct {
		statestore, protocol, errorMessage string
		status                             int
	}{
		{
			statestore:   "missingstore",
			protocol:     "http",
			errorMessage: "expected status code 204, got 400",
			status:       500,
		},
		{
			statestore:   "missingstore",
			protocol:     "grpc",
			errorMessage: "state store missingstore is not found",
			status:       500,
		},
		{
			statestore:   "badhost-store",
			protocol:     "http",
			errorMessage: "expected status code 204, got 400",
			status:       500,
		},
		{
			statestore:   "badhost-store",
			protocol:     "grpc",
			errorMessage: "state store badhost-store is not found",
			status:       500,
		},
		{
			statestore:   "badpass-store",
			protocol:     "http",
			errorMessage: "expected status code 204, got 400",
			status:       500,
		},
		{
			statestore:   "badpass-store",
			protocol:     "grpc",
			errorMessage: "state store badpass-store is not found",
			status:       500,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("store_%s_%s", tc.statestore, tc.protocol), func(t *testing.T) {
			// Define state URL for statestore that does not exist.
			stateURL := fmt.Sprintf("%s/test/%s/%s/%s", externalURL, tc.protocol, "save", tc.statestore)

			request := newRequest(utils.SimpleKeyValue{guuid.New().String(), nil})
			body, _ := json.Marshal(request)

			resp, status, err := utils.HTTPPostWithStatus(stateURL, body)

			// The state app doesn't persist the real error other than this message
			require.Contains(t, string(resp), tc.errorMessage)
			require.Equal(t, tc.status, status)
			require.Nil(t, err)
		})
	}
}
