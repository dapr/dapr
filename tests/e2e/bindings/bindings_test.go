// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

type testSendRequest struct {
	Messages []messageData `json:"messages,omitempty"`
}

type messageData struct {
	Data string `json:"data,omitempty"`
}

type receivedTopicsResponse struct {
	ReceivedMessages []string `json:"received_messages,omitempty"`
}

var testMessages = []string{
	"1",
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
		},
		{
			AppName:        "bindingoutput",
			DaprEnabled:    true,
			ImageName:      "e2e-binding_output",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("bindings", testApps, nil)
	os.Exit(tr.Start(m))
}

func TestBindings(t *testing.T) {
	outputExternalURL := tr.Platform.AcquireAppExternalURL("bindingoutput")
	require.NotEmpty(t, outputExternalURL, "bindingoutput external URL must not be empty!")
	inputExternalURL := tr.Platform.AcquireAppExternalURL("bindinginput")
	require.NotEmpty(t, inputExternalURL, "bindinginput external URL must not be empty!")

	var req testSendRequest
	for _, mes := range testMessages {
		req.Messages = append(req.Messages, messageData{Data: mes})
	}

	body, err := json.Marshal(req)
	require.NoError(t, err)

	_, err = utils.HTTPPost(fmt.Sprintf("%s/tests/send", outputExternalURL), body)
	require.NoError(t, err)

	resp, err := utils.HTTPPost(fmt.Sprintf("%s/tests/get_received_topics", inputExternalURL), nil)
	require.NoError(t, err)

	var decodedResponse receivedTopicsResponse
	err = json.Unmarshal(resp, &decodedResponse)
	require.NoError(t, err)
	require.Equal(t, testMessages, decodedResponse.ReceivedMessages)
}
