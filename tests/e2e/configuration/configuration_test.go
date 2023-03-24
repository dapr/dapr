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

package configuration_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/tests/utils/configupdater"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	appName             = "configurationapp"
	componentNameEnvVar = "DAPR_TEST_CONFIGURATION"
	v1                  = "1.0.0"
	numHealthChecks     = 60              // Number of times to check for endpoint health per app.
	defaultWaitTime     = 5 * time.Second // Time to wait for app to receive the updates
)

var (
	componentName       string = "redis"
	subscriptionId      string = ""
	runID               string = uuid.Must(uuid.NewRandom()).String()
	counter             int    = 0
	tr                  *runner.TestRunner
	updater             configupdater.Updater
	subscribedKeyValues map[string]*configuration.Item
)

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type receivedMessagesResponse struct {
	ReceivedUpdates []string `json:"received-messages"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

func TestMain(m *testing.M) {
	utils.SetupLogs("configuration e2e")
	utils.InitHTTPClient(true)

	p := os.Getenv(componentNameEnvVar)
	if p != "" {
		componentName = p
	}

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:           "configurationapp",
			DaprEnabled:       true,
			ImageName:         "e2e-configurationapp",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("configuration", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

var configurationTests = []struct {
	name    string
	handler func(t *testing.T, appExternalUrl string)
}{
	{
		name:    "testGet",
		handler: testGet,
	},
	{
		name:    "testSubscribe",
		handler: testSubscribe,
	},
	{
		name:    "testUnsubscribe",
		handler: testUnsubscribe,
	},
}

// Generates key-value pairs
func generateKeyValues(keyCount int, version string) map[string]*configuration.Item {
	m := make(map[string]*configuration.Item, keyCount)
	k := counter
	for ; k < counter+keyCount; k++ {
		key := runID + "-key-" + strconv.Itoa(k)
		val := runID + "-val-" + strconv.Itoa(k)
		m[key] = &configuration.Item{
			Value:    val,
			Version:  version,
			Metadata: map[string]string{},
		}
	}
	counter = k
	return m
}

// Updates `mymap` with new values for every key
func updateKeyValues(mymap map[string]*configuration.Item, version string) map[string]*configuration.Item {
	m := make(map[string]*configuration.Item, len(mymap))
	k := counter
	for key := range mymap {
		updatedVal := runID + "-val-" + strconv.Itoa(k)
		m[key] = &configuration.Item{
			Value:    updatedVal,
			Version:  version,
			Metadata: map[string]string{},
		}
		k++
	}
	counter = k
	return m
}

// returns the keys of a map
func getKeys(mymap map[string]*configuration.Item) []string {
	keys := []string{}
	for key := range mymap {
		keys = append(keys, key)
	}
	return keys
}

func testGet(t *testing.T, appExternalUrl string) {
	updateUrl := fmt.Sprintf("http://%s/update-key-values", appExternalUrl)
	items := generateKeyValues(10, v1)
	itemsInBytes, _ := json.Marshal(items)
	resp, statusCode, err := utils.HTTPPostWithStatus(updateUrl, itemsInBytes)
	require.NoError(t, err, "error updating key values")
	require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, string(resp))

	keys := getKeys(items)
	keysInBytes, _ := json.Marshal(keys)
	url := fmt.Sprintf("http://%s/get-key-values", appExternalUrl)
	resp, statusCode, err = utils.HTTPPostWithStatus(url, keysInBytes)
	require.NoError(t, err, "error getting key values")

	var appResp appResponse
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err, "error unmarshalling response")
	expectedItemsInBytes, _ := json.Marshal(items)
	expectedItems := string(expectedItemsInBytes)
	require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, appResp.Message)
	require.Equal(t, expectedItems, appResp.Message, "expected %s, got %s", expectedItems, appResp.Message)
}

func testSubscribe(t *testing.T, appExternalUrl string) {
	items := generateKeyValues(10, v1)
	keys := getKeys(items)
	keysInBytes, _ := json.Marshal(keys)
	url := fmt.Sprintf("http://%s/subscribe", appExternalUrl)
	resp, statusCode, err := utils.HTTPPostWithStatus(url, keysInBytes)
	require.NoError(t, err, "error subscribing to key values")
	subscribedKeyValues = items

	var appResp appResponse
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err, "error unmarshalling response")
	require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, appResp.Message)

	subscriptionId = appResp.Message

	updateUrl := fmt.Sprintf("http://%s/update-key-values", appExternalUrl)
	itemsInBytes, _ := json.Marshal(items)
	resp, statusCode, err = utils.HTTPPostWithStatus(updateUrl, itemsInBytes)
	require.NoError(t, err, "error updating key values")
	require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, string(resp))

	time.Sleep(defaultWaitTime)

	expectedUpdates := make([]string, len(items))
	for key, item := range items {
		update := map[string]*configuration.Item{
			key: item,
		}
		updateInBytes, err := json.Marshal(update)
		require.NoError(t, err, "error marshalling update")
		expectedUpdates = append(expectedUpdates, string(updateInBytes))
	}
	getMessagesUrl := fmt.Sprintf("http://%s/get-received-updates/%s", appExternalUrl, subscriptionId)
	getResp, err := utils.HTTPGet(getMessagesUrl)
	require.NoError(t, err, "error getting received messages")
	var receivedMessages receivedMessagesResponse
	err = json.Unmarshal(getResp, &receivedMessages)
	require.NoError(t, err, "error unmarshalling received messages response, got %s", string(getResp))
	require.ElementsMatch(t, expectedUpdates, receivedMessages.ReceivedUpdates, "expected %s, got %s", expectedUpdates, receivedMessages.ReceivedUpdates)
}

func testUnsubscribe(t *testing.T, appExternalUrl string) {
	url := fmt.Sprintf("http://%s/unsubscribe/%s", appExternalUrl, subscriptionId)
	_, err := utils.HTTPGet(url)
	require.NoError(t, err, "error unsubscribing to key values")

	items := updateKeyValues(subscribedKeyValues, v1)
	updateUrl := fmt.Sprintf("http://%s/update-key-values", appExternalUrl)
	itemsInBytes, _ := json.Marshal(items)
	resp, statusCode, err := utils.HTTPPostWithStatus(updateUrl, itemsInBytes)
	require.NoError(t, err, "error updating key values")
	require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, string(resp))

	time.Sleep(defaultWaitTime)

	expectedUpdates := make([]string, len(items))
	getMessagesUrl := fmt.Sprintf("http://%s/get-received-updates/%s", appExternalUrl, subscriptionId)
	getResp, err := utils.HTTPGet(getMessagesUrl)
	require.NoError(t, err, "error getting received messages")
	var receivedMessages receivedMessagesResponse
	err = json.Unmarshal(getResp, &receivedMessages)
	require.NoError(t, err, "error unmarshalling received messages response, got %s", string(getResp))
	require.Equal(t, expectedUpdates, receivedMessages.ReceivedUpdates, "expected %s, got %s", expectedUpdates, receivedMessages.ReceivedUpdates)
}

var apps []struct {
	name string
} = []struct {
	name string
}{
	{
		name: appName,
	},
}

func TestConfiguration(t *testing.T) {
	for _, app := range apps {
		// Get the ingress external url of test app
		externalURL := tr.Platform.AcquireAppExternalURL(app.name)
		require.NotEmpty(t, externalURL, "external URL must not be empty")

		// Check if test app endpoint is available
		_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
		require.NoError(t, err)

		// Initialize the configuration updater
		url := fmt.Sprintf("http://%s/initialize-updater", externalURL)
		componentNameInBytes, _ := json.Marshal(componentName)
		resp, statusCode, err := utils.HTTPPostWithStatus(url, componentNameInBytes)
		require.NoError(t, err, "error initializing configuration updater")
		require.Equal(t, 200, statusCode, "expected statuscode 200, got %d. Error: %s", statusCode, string(resp))

		for _, tt := range configurationTests {
			t.Run(tt.name, func(t *testing.T) {
				tt.handler(t, externalURL)
			})
		}
	}
}
