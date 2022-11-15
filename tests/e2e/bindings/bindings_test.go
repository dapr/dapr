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

package bindings_e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"

	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
)

type testSendRequest struct {
	Messages []messageData `json:"messages,omitempty"`
}

type messageData struct {
	Data      string `json:"data,omitempty"`
	Operation string `json:"operation"`
}

type receivedTopicsResponse struct {
	ReceivedMessages []string `json:"received_messages,omitempty"`
	FailedMessage    string   `json:"failed_message,omitempty"`
	RoutedMessages   []string `json:"routeed_messages,omitempty"`
}

var testMessages = []string{
	"This message fails",
	"2",
	"3",
	"4",
	"5",
	"6",
	"7",
	"8",
	"9",
	"10",
}

const (
	// Number of times to call the endpoint to check for health.
	numHealthChecks = 60
	// Number of seconds to wait for binding travelling throughout the cluster.
	inputBindingAppName                  = "bindinginput"
	outputBindingAppName                 = "bindingoutput"
	inputBindingGRPCAppName              = "bindinginputgrpc"
	e2eInputBindingImage                 = "e2e-binding_input"
	e2eOutputBindingImage                = "e2e-binding_output"
	e2eInputBindingGRPCImage             = "e2e-binding_input_grpc"
	inputBindingPluggableAppName         = "pluggable-bindinginput"
	outputbindingPluggableAppName        = "pluggable-bindingoutput"
	inputBindingGRPCPluggableAppName     = "pluggable-bindinginputgrpc"
	kafkaBindingsPluggableComponentImage = "e2e-pluggable_kafka-bindings"
	DaprTestTopicEnvVar                  = "DAPR_TEST_TOPIC_NAME"
	DaprTestGRPCTopicEnvVar              = "DAPR_TEST_GRPC_TOPIC_NAME"
	DaprTestInputBindingServiceEnVar     = "DAPR_TEST_INPUT_BINDING_SVC"
	DaprTestCustomPathRouteEnvVar        = "DAPR_TEST_CUSTOM_PATH_ROUTE"
	bindingPropagationDelay              = 10
)

var tr *runner.TestRunner

var bindingsApps []struct {
	suite        string
	inputApp     string
	inputGRPCApp string
	outputApp    string
} = []struct {
	suite        string
	inputApp     string
	inputGRPCApp string
	outputApp    string
}{
	{
		suite:        "built-in",
		inputApp:     inputBindingAppName,
		inputGRPCApp: inputBindingGRPCAppName,
		outputApp:    outputBindingAppName,
	},
}

func TestMain(m *testing.M) {
	utils.SetupLogs("bindings")
	utils.InitHTTPClient(true)

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        inputBindingAppName,
			DaprEnabled:    true,
			ImageName:      e2eInputBindingImage,
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        outputBindingAppName,
			DaprEnabled:    true,
			ImageName:      e2eOutputBindingImage,
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        inputBindingGRPCAppName,
			DaprEnabled:    true,
			ImageName:      e2eInputBindingGRPCImage,
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	if utils.TestTargetOS() != "windows" { // pluggable components feature requires unix socket to work
		pluggableComponents := []apiv1.Container{
			{
				Name:  "kafka-pluggable",
				Image: runner.BuildTestImageName(kafkaBindingsPluggableComponentImage),
			},
		}
		appEnv := map[string]string{
			DaprTestGRPCTopicEnvVar:          "pluggable-test-topic-grpc",
			DaprTestTopicEnvVar:              "pluggable-test-topic",
			DaprTestInputBindingServiceEnVar: "pluggable-bindinginputgrpc",
			DaprTestCustomPathRouteEnvVar:    "pluggable-custom-path",
		}
		testApps = append(testApps, []kube.AppDescription{
			{
				AppName:             inputBindingPluggableAppName,
				DaprEnabled:         true,
				ImageName:           e2eInputBindingImage,
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				PluggableComponents: pluggableComponents,
				AppEnv:              appEnv,
			},
			{
				AppName:             outputbindingPluggableAppName,
				DaprEnabled:         true,
				ImageName:           e2eOutputBindingImage,
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				PluggableComponents: pluggableComponents,
				AppEnv:              appEnv,
			},
			{
				AppName:             inputBindingGRPCPluggableAppName,
				DaprEnabled:         true,
				ImageName:           e2eInputBindingGRPCImage,
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				AppProtocol:         "grpc",
				PluggableComponents: pluggableComponents,
				AppEnv:              appEnv,
			},
		}...)
		bindingsApps = append(bindingsApps, struct {
			suite        string
			inputApp     string
			inputGRPCApp string
			outputApp    string
		}{
			suite:        "pluggable",
			inputApp:     inputBindingPluggableAppName,
			inputGRPCApp: inputBindingGRPCPluggableAppName,
			outputApp:    outputbindingPluggableAppName,
		})
	}

	tr = runner.NewTestRunner("bindings", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func testBindingsForApp(app struct {
	suite        string
	inputApp     string
	inputGRPCApp string
	outputApp    string
},
) func(t *testing.T) {
	return func(t *testing.T) {
		// setup
		outputExternalURL := tr.Platform.AcquireAppExternalURL(app.outputApp)
		require.NotEmpty(t, outputExternalURL, "bindingoutput external URL must not be empty!")
		inputExternalURL := tr.Platform.AcquireAppExternalURL(app.inputApp)
		require.NotEmpty(t, inputExternalURL, "bindinginput external URL must not be empty!")
		inputGRPCExternalURL := tr.Platform.AcquireAppExternalURL(app.inputGRPCApp)
		require.NotEmpty(t, inputGRPCExternalURL, "bindinginput external URL must not be empty!")
		// This initial probe makes the test wait a little bit longer when needed,
		// making this test less flaky due to delays in the deployment.
		_, err := utils.HTTPGetNTimes(outputExternalURL, numHealthChecks)
		require.NoError(t, err)
		_, err = utils.HTTPGetNTimes(inputExternalURL, numHealthChecks)
		require.NoError(t, err)

		var req testSendRequest
		for _, mes := range testMessages {
			req.Messages = append(req.Messages, messageData{Data: mes, Operation: "create"})
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)

		// act for http
		httpPostWithAssert(t, fmt.Sprintf("%s/tests/send", outputExternalURL), body, http.StatusOK)

		// This delay allows all the messages to reach corresponding input bindings.
		time.Sleep(bindingPropagationDelay * time.Second)

		// assert for HTTP
		resp := httpPostWithAssert(t, fmt.Sprintf("%s/tests/get_received_topics", inputExternalURL), nil, http.StatusOK)

		var decodedResponse receivedTopicsResponse
		err = json.Unmarshal(resp, &decodedResponse)
		require.NoError(t, err)

		// Only the first message fails, all other messages are successfully consumed.
		// nine messages succeed.
		require.Equal(t, testMessages[1:], decodedResponse.ReceivedMessages)
		// one message fails.
		require.Equal(t, testMessages[0], decodedResponse.FailedMessage)
		// routed binding will receive all messages
		require.Equal(t, testMessages[0:], decodedResponse.RoutedMessages)

		// act for gRPC
		httpPostWithAssert(t, fmt.Sprintf("%s/tests/sendGRPC", outputExternalURL), body, http.StatusOK)

		// This delay allows all the messages to reach corresponding input bindings.
		time.Sleep(bindingPropagationDelay * time.Second)

		// assert for gRPC
		resp = httpPostWithAssert(t, fmt.Sprintf("%s/tests/get_received_topics_grpc", outputExternalURL), nil, http.StatusOK)

		// assert for gRPC
		err = json.Unmarshal(resp, &decodedResponse)
		require.NoError(t, err)

		// Only the first message fails, all other messages are successfully consumed.
		// nine messages succeed.
		require.Equal(t, testMessages[1:], decodedResponse.ReceivedMessages)
		// one message fails.
		require.Equal(t, testMessages[0], decodedResponse.FailedMessage)
	}
}

func TestBindings(t *testing.T) {
	for idx := range bindingsApps {
		app := bindingsApps[idx]
		t.Run(app.suite, testBindingsForApp(app))
	}
}

func httpPostWithAssert(t *testing.T, url string, data []byte, status int) []byte {
	resp, code, err := utils.HTTPPostWithStatus(url, data)
	require.NoError(t, err)
	require.Equal(t, status, code)
	return resp
}
