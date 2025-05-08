//go:build e2e

/*
Copyright 2025 The Dapr Authors
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

package pubsub_streaming

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	publisherStreamingAppName  = "pubsub-publisher-streaming"
	subscriberStreamingAppName = "pubsub-subscriber-streaming"
	pubsubInMemoryName         = "inmemory-pubsub-streaming"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_streaming")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:        publisherStreamingAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-publisher-streaming",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        subscriberStreamingAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-subscriber-streaming",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	components := []kube.ComponentDescription{
		{
			Name:      pubsubInMemoryName,
			Namespace: &kube.DaprTestNamespace,
			TypeName:  "pubsub.in-memory",
			MetaData: map[string]kube.MetadataValue{
				"version":  {Raw: `"v1"`},
				"metadata": {},
			},
			Scopes: []string{publisherStreamingAppName, subscriberStreamingAppName},
		},
	}

	tr = runner.NewTestRunner("pubsub_streaming", testApps, components, nil)
	os.Exit(tr.Start(m))
}

var pubsubStreamingTests = []struct {
	testName string
	count    int
	handler  func(*testing.T, string, string, int)
}{
	{
		testName: "publish and subscribe in-memory message order - 10000",
		count:    10000,
		handler:  testInMemoryPubsubStreaming,
	},
}

func TestPubSubStreaming(t *testing.T) {
	publisherURL := tr.Platform.AcquireAppExternalURL(publisherStreamingAppName)
	require.NotEmpty(t, publisherURL, "publisherURL must not be empty!")
	subscriberURL := tr.Platform.AcquireAppExternalURL(subscriberStreamingAppName)
	require.NotEmpty(t, subscriberURL, "subscriberURL must not be empty!")

	for _, tt := range pubsubStreamingTests {
		t.Run(tt.testName, func(t *testing.T) {
			tt.handler(t, publisherURL, subscriberURL, tt.count)
		})
	}
}

func testInMemoryPubsubStreaming(t *testing.T, publisherURL, subscriberURL string, numberOfMessages int) {
	log.Println("Test publish subscribe in-memory messaging order with count: " + strconv.Itoa(numberOfMessages))
	subscribeTestURL := fmt.Sprintf("http://%s/tests/streaming-order-in-memory-subscribe?count=%d", subscriberURL, numberOfMessages)
	body, err := json.Marshal(map[string]string{})
	require.NoError(t, err)

	responseBody, status, err := utils.HTTPPostWithStatus(subscribeTestURL, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	var response struct {
		SentCount        int32
		ReceivedCount    int32
		SentMessages     []int
		ReceivedMessages []int
	}
	err = json.Unmarshal(responseBody, &response)
	require.NoError(t, err)

	log.Printf("sent count: %d, received count: %d", response.SentCount, response.ReceivedCount)
	assert.Equal(t, int32(numberOfMessages), response.SentCount)
	assert.Equal(t, int32(numberOfMessages), response.ReceivedCount)

	assert.Equal(t, response.SentMessages, response.ReceivedMessages)
}
