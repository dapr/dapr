// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package hellodapr_e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// This test shows how to deploy the multiple test apps, validate the side-car injection
	// and validate the response by using test app's service endpoint

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "hellobluedapr",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

var helloAppTests = []struct {
	in               string
	app              string
	testCommand      string
	expectedResponse string
}{
	{
		"green dapr",
		"hellogreendapr",
		"green",
		"Hello green dapr!",
	},
	{
		"blue dapr",
		"hellobluedapr",
		"blue",
		"Hello blue dapr!",
	},
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
				},
			},
		},
	}
}

func sendToTransaction(t *testing.T, publisherExternalURL string, topic string) ([]string, error) {
	var sentMessages []string
	commandBody := publishCommand{Topic: topic}
	offset := rand.Intn(randomOffsetMax)
	for i := offset; i < offset+numberOfMessagesToPublish; i++ {
		commandBody.Data = fmt.Sprintf("message-%d", i)

		sentMessages = append(sentMessages, commandBody.Data)
		jsonValue, err := json.Marshal(commandBody)
		require.NoError(t, err)

		// this is the publish app's endpoint, not a dapr endpoint
		url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

		// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
		if i == offset {
			log.Printf("Sending first publish app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)
		}

		postResp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonValue))
		require.NoError(t, err)
		if err != nil {
			if postResp != nil {
				log.Printf("Publish failed with error=%s, StatusCode=%d", err.Error(), postResp.StatusCode)
			} else {
				log.Printf("Publish failed with error=%s, response is nil", err.Error())
			}

			return nil, err
		}
	}

	return sentMessages, nil
}
func TestHelloDapr(t *testing.T) {
	for _, tt := range helloAppTests {
		t.Run(tt.in, func(t *testing.T) {
			// Get the ingress external url of test app
			externalURL := tr.Platform.AcquireAppExternalURL(tt.app)
			require.NotEmpty(t, externalURL, "external URL must not be empty")

			// Check if test app endpoint is available
			resp, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			// Trigger test
			body, err := json.Marshal(testCommandRequest{
				Message: "Hello Dapr.",
			})
			require.NoError(t, err)

			resp, err = utils.HTTPPost(fmt.Sprintf("%s/tests/%s", externalURL, tt.testCommand), body)
			require.NoError(t, err)

			var appResp appResponse
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResponse, appResp.Message)
		})
	}
}

func TestScaleReplicas(t *testing.T) {
	err := tr.Platform.Scale("hellobluedapr", 3)
	require.NoError(t, err, "fails to scale hellobluedapr app to 3 replicas")
}

func TestScaleAndRestartInstances(t *testing.T) {
	err := tr.Platform.Scale("hellobluedapr", 3)
	require.NoError(t, err, "fails to scale hellobluedapr app to 3 replicas")

	err = tr.Platform.Restart("hellobluedapr")
	require.NoError(t, err, "fails to restart hellobluedapr pods")
}
