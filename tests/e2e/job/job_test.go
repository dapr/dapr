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

package job

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

type callSubscriberMethodRequest struct {
	RemoteApp string `json:"remoteApp"`
	Protocol  string `json:"protocol"`
	Method    string `json:"method"`
}

// data returned from the subscriber app.
type receivedMessagesResponse struct {
	ReceivedByTopicJob []string `json:"pubsub-job-topic"`
}

const (
	receiveMessageRetries = 25

	publisherAppName  = "job-publisher"
	subscriberAppName = "job-subscriber"
)

func TestMain(m *testing.M) {
	utils.SetupLogs("job")
	utils.InitHTTPClient(true)

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        subscriberAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-subscriber",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        publisherAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-job-publisher",
			Replicas:       1,
			IngressEnabled: false,
			IsJob:          true,
		},
	}

	tr = runner.NewTestRunner("job", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestJobPublishMessage(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/getMessages", externalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberAppName,
		Protocol:  "http",
		Method:    "getMessages",
	}

	rawReq, _ := json.Marshal(request)

	var appResp receivedMessagesResponse
	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		resp, err := utils.HTTPPost(url, rawReq)
		if err != nil {
			continue
		}

		err = json.Unmarshal(resp, &appResp)
		if err != nil {
			continue
		}

		log.Printf("Subscriber receieved %d messages on pubsub-job-topic-http", len(appResp.ReceivedByTopicJob))

		if len(appResp.ReceivedByTopicJob) == 0 {
			log.Printf("No message received, retrying.")
			time.Sleep(2 * time.Second)
		} else {
			break
		}
	}

	require.Len(t, appResp.ReceivedByTopicJob, 1)
	require.Equal(t, "message-from-job", appResp.ReceivedByTopicJob[0])
}
