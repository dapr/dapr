// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsubapp_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60

	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 99
	numberOfMessagesToPublish = 100

	publisherAppName  = "pubsub-publisher"
	subscriberAppName = "pubsub-subscriber"
)

// sent to the publisher app, which will publish data to dapr
type publishCommand struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

// data returned from the subscriber app
type receivedMessagesResponse struct {
	ReceivedByTopicA []string `json:"pubsub-a-topic"`
	ReceivedByTopicB []string `json:"pubsub-b-topic"`
	ReceivedByTopicC []string `json:"pubsub-c-topic"`
}

// sends messages to the publisher app.  The publisher app does the actual publish
func sendToPublisher(t *testing.T, publisherExternalURL string, topic string) ([]string, error) {
	var sentMessages []string
	commandBody := publishCommand{Topic: topic}
	offset := rand.Intn(randomOffsetMax)
	for i := offset; i < offset+numberOfMessagesToPublish; i++ {
		// create and marshal message
		commandBody.Data = fmt.Sprintf("message-%d", i)
		jsonValue, err := json.Marshal(commandBody)
		require.NoError(t, err)

		// this is the publish app's endpoint, not a dapr endpoint
		url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

		// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
		if i == offset {
			log.Printf("Sending first publish app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)
		}

		statusCode, err := postSingleMessage(url, jsonValue)
		// return on an unsuccessful publish
		if statusCode != http.StatusNoContent {
			return nil, err
		}

		// save successful message
		sentMessages = append(sentMessages, commandBody.Data)
	}

	return sentMessages, nil
}

func testPublish(t *testing.T, publisherExternalURL string) receivedMessagesResponse {
	var err error
	sentTopicAMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-a-topic")
	require.NoError(t, err)

	sentTopicBMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-b-topic")
	require.NoError(t, err)

	sentTopicCMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-c-topic")
	require.NoError(t, err)

	return receivedMessagesResponse{
		ReceivedByTopicA: sentTopicAMessages,
		ReceivedByTopicB: sentTopicBMessages,
		ReceivedByTopicC: sentTopicCMessages,
	}
}

func postSingleMessage(url string, data []byte) (int, error) {
	// HTTPPostWithStatus by default sends with content-type application/json
	_, statusCode, err := utils.HTTPPostWithStatus(url, data)
	if err != nil {
		log.Printf("Publish failed with error=%s, response is nil", err.Error())
		return http.StatusInternalServerError, err
	}
	if statusCode != http.StatusOK {
		err = fmt.Errorf("publish failed with StatusCode=%d", statusCode)
	}
	return statusCode, err
}

func testPublishSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, _ string) string {
	log.Printf("Test publish subscribe success flow\n")
	sentMessages := testPublish(t, publisherExternalURL)

	time.Sleep(5 * time.Second)
	validateMessagesReceivedBySubscriber(t, subscriberExternalURL, sentMessages)
	return subscriberExternalURL
}

func testPublishWithoutTopic(t *testing.T, publisherExternalURL, subscriberExternalURL, _, _ string) string {
	log.Printf("Test publish without topic\n")
	commandBody := publishCommand{}
	commandBody.Data = "unsuccessful message"
	jsonValue, err := json.Marshal(commandBody)
	require.NoError(t, err)
	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

	// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
	log.Printf("Sending first publish app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)

	statusCode, err := postSingleMessage(url, jsonValue)
	require.Error(t, err)
	// without topic, response should be 404
	require.Equal(t, http.StatusNotFound, statusCode)
	return subscriberExternalURL
}

func testValidateRedeliveryOrEmptyJSON(t *testing.T, publisherExternalURL, subscriberExternalURL, subscriberResponse, subscriberAppName string) string {
	log.Printf("Set subscriber to respond with %s\n", subscriberResponse)
	if subscriberResponse == "empty-json" {
		log.Println("Initialize the sets again in the subscriber application for this scenario ...")
		// only for the empty-json scenario, initialize empty sets in the subscriber app
		_, code, err := utils.HTTPPostWithStatus(subscriberExternalURL+"/tests/initialize", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, code)
	}

	// set to respond with specified subscriber response
	_, code, err := utils.HTTPPostWithStatus(subscriberExternalURL+"/tests/set-respond-"+subscriberResponse, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	sentMessages := testPublish(t, publisherExternalURL)

	if subscriberResponse == "empty-json" {
		// on empty-json response case immediately validate the received messages
		time.Sleep(5 * time.Second)
		validateMessagesReceivedBySubscriber(t, subscriberExternalURL, sentMessages)
	}

	// restart application
	log.Printf("Restarting subscriber application to check redelivery...\n")
	err = tr.Platform.Restart(subscriberAppName)
	require.NoError(t, err, "error restarting subscriber")
	subscriberExternalURL = tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")
	_, err = utils.HTTPGetNTimes(subscriberExternalURL, numHealthChecks)
	require.NoError(t, err)

	if subscriberResponse == "empty-json" {
		// validate that there is no redelivery of messages
		log.Printf("Validating no redelivered messages...")
		time.Sleep(5 * time.Second)
		validateMessagesReceivedBySubscriber(t, subscriberExternalURL, receivedMessagesResponse{
			// empty string slices
			ReceivedByTopicA: []string{},
			ReceivedByTopicB: []string{},
			ReceivedByTopicC: []string{},
		})
	} else {
		// validate redelivery of messages
		log.Printf("Validating redelivered messages...")
		time.Sleep(5 * time.Second)
		validateMessagesReceivedBySubscriber(t, subscriberExternalURL, sentMessages)
	}
	return subscriberExternalURL
}

func validateMessagesReceivedBySubscriber(t *testing.T, subscriberExternalURL string, sentMessages receivedMessagesResponse) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/get", subscriberExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	resp, err := utils.HTTPPost(url, nil)
	require.NoError(t, err)

	var appResp receivedMessagesResponse
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err)

	log.Printf("subscriber receieved %d messages on pubsub-a-topic, %d on pubsub-b-topic and %d on pubsub-c-topic", len(appResp.ReceivedByTopicA), len(appResp.ReceivedByTopicB), len(appResp.ReceivedByTopicC))

	// Sort messages first because the delivered messages cannot be ordered.
	sort.Strings(sentMessages.ReceivedByTopicA)
	sort.Strings(appResp.ReceivedByTopicA)
	sort.Strings(sentMessages.ReceivedByTopicB)
	sort.Strings(appResp.ReceivedByTopicB)
	sort.Strings(sentMessages.ReceivedByTopicC)
	sort.Strings(appResp.ReceivedByTopicC)

	if !reflect.DeepEqual(sentMessages.ReceivedByTopicA, appResp.ReceivedByTopicA) {
		for i := 0; i < len(sentMessages.ReceivedByTopicA); i++ {
			log.Printf("%s, %s", sentMessages.ReceivedByTopicA[i], appResp.ReceivedByTopicA[i])
		}
	}
	require.Equal(t, sentMessages.ReceivedByTopicA, appResp.ReceivedByTopicA)
	require.Equal(t, sentMessages.ReceivedByTopicB, appResp.ReceivedByTopicB)
	require.Equal(t, sentMessages.ReceivedByTopicC, appResp.ReceivedByTopicC)
}

func TestMain(m *testing.M) {
	fmt.Println("Enter TestMain")
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        publisherAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-publisher",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        subscriberAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-subscriber",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("pubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

var pubsubTests = []struct {
	name               string
	handler            func(*testing.T, string, string, string, string) string
	subscriberResponse string
}{
	{
		name:    "publish and subscribe message successfully",
		handler: testPublishSubscribeSuccessfully,
	},
	{
		name:               "publish with subscriber returning empty json test delivery of message once",
		handler:            testValidateRedeliveryOrEmptyJSON,
		subscriberResponse: "empty-json",
	},
	{
		name:    "publish with no topic",
		handler: testPublishWithoutTopic,
	},
	{
		name:               "publish with subscriber error test redelivery of messages",
		handler:            testValidateRedeliveryOrEmptyJSON,
		subscriberResponse: "error",
	},
	{
		name:               "publish with subscriber retry test redelivery of messages",
		handler:            testValidateRedeliveryOrEmptyJSON,
		subscriberResponse: "retry",
	},
	{
		name:               "publish with subscriber invalid status test redelivery of messages",
		handler:            testValidateRedeliveryOrEmptyJSON,
		subscriberResponse: "invalid-status",
	},
}

func TestPubSub(t *testing.T) {
	t.Log("Enter TestPubSub")
	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	subscriberExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(publisherExternalURL, numHealthChecks)
	require.NoError(t, err)

	_, err = utils.HTTPGetNTimes(subscriberExternalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tc := range pubsubTests {
		t.Run(tc.name, func(t *testing.T) {
			subscriberExternalURL = tc.handler(t, publisherExternalURL, subscriberExternalURL, tc.subscriberResponse, subscriberAppName)
		})
	}
}
