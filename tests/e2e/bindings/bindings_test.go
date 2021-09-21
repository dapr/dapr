//go:build e2e
// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"

	"github.com/stretchr/testify/require"
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

	receiveMessageRetries = 20
	receiveMessageSleep   = 1 * time.Second
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "bindinginput",
			DaprEnabled:    true,
			ImageName:      "e2e-binding_input",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "bindingoutput",
			DaprEnabled:    true,
			ImageName:      "e2e-binding_output",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "bindinginputgrpc",
			DaprEnabled:    true,
			ImageName:      "e2e-binding_input_grpc",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	tr = runner.NewTestRunner("bindings", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestBindings(t *testing.T) {
	// setup
	outputExternalURL := tr.Platform.AcquireAppExternalURL("bindingoutput")
	require.NotEmpty(t, outputExternalURL, "bindingoutput external URL must not be empty!")
	inputExternalURL := tr.Platform.AcquireAppExternalURL("bindinginput")
	require.NotEmpty(t, inputExternalURL, "bindinginput external URL must not be empty!")
	inputGRPCExternalURL := tr.Platform.AcquireAppExternalURL("bindinginputgrpc")
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

	var decodedResponse receivedTopicsResponse

	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		if retryCount > 0 {
			log.Printf("Differing lengths in received vs. sent HTTP messages, retrying.")
		}

		// This delay allows all the messages to reach corresponding input bindings.
		time.Sleep(receiveMessageSleep)

		// assert for HTTP
		resp := httpPostWithAssert(t, fmt.Sprintf("%s/tests/get_received_topics", inputExternalURL), nil, http.StatusOK)
		err = json.Unmarshal(resp, &decodedResponse)
		require.NoError(t, err)

		numReceivedMessages := len(decodedResponse.ReceivedMessages)
		numFailedMessages := len(decodedResponse.FailedMessage)
		totalReceived := numReceivedMessages + numFailedMessages

		log.Printf("binding HTTP messages: received %d, failed %d", numReceivedMessages, numFailedMessages)

		if totalReceived == len(testMessages) {
			break
		}
	}

	// Only the first message fails, all other messages are successfully consumed.
	// nine messages succeed.
	require.Equal(t, testMessages[1:], decodedResponse.ReceivedMessages)
	// one message fails.
	require.Equal(t, testMessages[0], decodedResponse.FailedMessage)
	// routed binding will receive all messages
	require.Equal(t, testMessages[0:], decodedResponse.RoutedMessages)

	// act for gRPC
	httpPostWithAssert(t, fmt.Sprintf("%s/tests/sendGRPC", outputExternalURL), body, http.StatusOK)

	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		if retryCount > 0 {
			log.Printf("Differing lengths in received vs. sent gRPC messages, retrying.")
		}

		// This delay allows all the messages to reach corresponding input bindings.
		time.Sleep(receiveMessageSleep)

		// assert for HTTP
		resp := httpPostWithAssert(t, fmt.Sprintf("%s/tests/get_received_topics_grpc", outputExternalURL), nil, http.StatusOK)
		err = json.Unmarshal(resp, &decodedResponse)
		require.NoError(t, err)

		numReceivedMessages := len(decodedResponse.ReceivedMessages)
		numFailedMessages := len(decodedResponse.FailedMessage)
		totalReceived := numReceivedMessages + numFailedMessages

		log.Printf("binding gRPC messages: received %d, failed %d", numReceivedMessages, numFailedMessages)

		if totalReceived == len(testMessages) {
			break
		}
	}

	// Only the first message fails, all other messages are successfully consumed.
	// nine messages succeed.
	require.Equal(t, testMessages[1:], decodedResponse.ReceivedMessages)
	// one message fails.
	require.Equal(t, testMessages[0], decodedResponse.FailedMessage)
}

func httpPostWithAssert(t *testing.T, url string, data []byte, status int) []byte {
	resp, code, err := utils.HTTPPostWithStatus(url, data)
	require.NoError(t, err)
	require.Equal(t, status, code)
	return resp
}
