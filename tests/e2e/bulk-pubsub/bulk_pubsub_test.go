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

package bulkpubsubapp_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60

	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 49
	numberOfMessagesToPublish = 60
	publishRateLimitRPS       = 25

	receiveMessageRetries = 5

	publisherAppName      = "pubsub-publisher-bulk-subscribe"
	bulkSubscriberAppName = "pubsub-bulk-subscriber"
	PubSubEnvVar          = "DAPR_TEST_PUBSUB_NAME"
)

var (
	offset     int
	pubsubName string
)

// sent to the publisher app, which will publish data to dapr.
type publishCommand struct {
	ReqID       string            `json:"reqID"`
	ContentType string            `json:"contentType"`
	Topic       string            `json:"topic"`
	Data        interface{}       `json:"data"`
	Protocol    string            `json:"protocol"`
	Metadata    map[string]string `json:"metadata"`
}

type callSubscriberMethodRequest struct {
	ReqID     string `json:"reqID"`
	RemoteApp string `json:"remoteApp"`
	Protocol  string `json:"protocol"`
	Method    string `json:"method"`
}

// data returned from the subscriber app.
type receivedBulkMessagesResponse struct {
	ReceivedByTopicRawSub     []string `json:"pubsub-raw-sub-topic"`
	ReceivedByTopicCESub      []string `json:"pubsub-ce-sub-topic"`
	ReceivedByTopicRawBulkSub []string `json:"pubsub-raw-bulk-sub-topic"`
	ReceivedByTopicCEBulkSub  []string `json:"pubsub-ce-bulk-sub-topic"`
	ReceivedByTopicDead       []string `json:"pubsub-dead-bulk-topic"`
	ReceivedByTopicDeadLetter []string `json:"pubsub-deadletter-bulk-topic"`
}

type cloudEvent struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	DataContentType string `json:"datacontenttype"`
	Data            string `json:"data"`
}

// checks is publishing is working.
func publishHealthCheck(publisherExternalURL string) error {
	commandBody := publishCommand{
		ContentType: "application/json",
		Topic:       "pubsub-healthcheck-topic-http",
		Protocol:    "http",
		Data:        "health check",
	}

	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

	return backoff.Retry(func() error {
		commandBody.ReqID = "c-" + uuid.New().String()
		jsonValue, _ := json.Marshal(commandBody)
		_, err := postSingleMessage(url, jsonValue)
		return err
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
}

// sends messages to the publisher app.  The publisher app does the actual publish.
func sendToPublisher(t *testing.T, publisherExternalURL string, topic string, protocol string, metadata map[string]string, cloudEventType string) ([]string, error) {
	var sentMessages []string
	contentType := "application/json"
	if cloudEventType != "" {
		contentType = "application/cloudevents+json"
	}
	commandBody := publishCommand{
		ContentType: contentType,
		Topic:       fmt.Sprintf("%s-%s", topic, protocol),
		Protocol:    protocol,
		Metadata:    metadata,
	}
	rateLimit := ratelimit.New(publishRateLimitRPS)
	for i := offset; i < offset+numberOfMessagesToPublish; i++ {
		// create and marshal message
		messageID := fmt.Sprintf("msg-%s-%s-%04d", strings.TrimSuffix(topic, "-topic"), protocol, i)
		var messageData interface{} = messageID
		if cloudEventType != "" {
			messageData = &cloudEvent{
				ID:              messageID,
				Type:            cloudEventType,
				DataContentType: "text/plain",
				Data:            messageID,
			}
		}
		commandBody.ReqID = "c-" + uuid.New().String()
		commandBody.Data = messageData
		jsonValue, err := json.Marshal(commandBody)
		require.NoError(t, err)

		// this is the publish app's endpoint, not a dapr endpoint
		url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

		// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
		if i == offset {
			log.Printf("Sending first publish app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)
		}

		rateLimit.Take()
		statusCode, err := postSingleMessage(url, jsonValue)
		// return on an unsuccessful publish
		if statusCode != http.StatusNoContent {
			return nil, err
		}

		// save successful message
		sentMessages = append(sentMessages, messageID)
	}

	return sentMessages, nil
}

func testPublishForBulkSubscribe(t *testing.T, publisherExternalURL string, protocol string) receivedBulkMessagesResponse {
	sentTopicCESubMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-ce-sub-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	sentTopicCEBulkSubMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-ce-bulk-sub-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	metadata := map[string]string{
		"rawPayload": "true",
	}
	sentTopicRawSubMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-raw-sub-topic", protocol, metadata, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	sentTopicRawBulkSubMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-raw-bulk-sub-topic", protocol, metadata, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	return receivedBulkMessagesResponse{
		ReceivedByTopicRawSub:     sentTopicRawSubMessages,
		ReceivedByTopicCESub:      sentTopicCESubMessages,
		ReceivedByTopicRawBulkSub: sentTopicRawBulkSubMessages,
		ReceivedByTopicCEBulkSub:  sentTopicCEBulkSubMessages,
	}
}

func postSingleMessage(url string, data []byte) (int, error) {
	// HTTPPostWithStatus by default sends with content-type application/json
	start := time.Now()
	_, statusCode, err := utils.HTTPPostWithStatus(url, data)
	if err != nil {
		log.Printf("Publish failed with error=%s (body=%s) (duration=%s)", err.Error(), data, utils.FormatDuration(time.Now().Sub(start)))
		return http.StatusInternalServerError, err
	}
	if (statusCode != http.StatusOK) && (statusCode != http.StatusNoContent) {
		err = fmt.Errorf("publish failed with StatusCode=%d (body=%s) (duration=%s)", statusCode, data, utils.FormatDuration(time.Now().Sub(start)))
	}
	return statusCode, err
}

func testPublishBulkSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, bulkSubscriberAppName, protocol string) string {
	callInitialize(t, bulkSubscriberAppName, publisherExternalURL, protocol)
	setDesiredResponse(t, bulkSubscriberAppName, "success", publisherExternalURL, protocol)

	log.Printf("Test publish bulk subscribe success flow\n")
	sentMessages := testPublishForBulkSubscribe(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateMessagesReceivedWhenSomeTopicsBulkSubscribed(t, publisherExternalURL, bulkSubscriberAppName, protocol, false, sentMessages)
	return subscriberExternalURL
}

func testDropToDeadLetter(t *testing.T, publisherExternalURL, subscriberExternalURL, _, bulkSubscriberAppName, protocol string) string {
	callInitialize(t, bulkSubscriberAppName, publisherExternalURL, protocol)
	setDesiredResponse(t, bulkSubscriberAppName, "drop", publisherExternalURL, protocol)

	// send messages to topic that has dead lettering enabled
	sentTopicDeadMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-dead-bulk-sub-topic-http", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	// send messages to topic that has no dead letter, messages should be dropped
	sentTopicCEMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-ce-bulk-sub-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	time.Sleep(5 * time.Second)
	validateMessagesReceivedWhenSomeTopicsBulkSubscribed(t, publisherExternalURL, bulkSubscriberAppName, protocol, true,
		receivedBulkMessagesResponse{
			ReceivedByTopicRawSub:     []string{},
			ReceivedByTopicRawBulkSub: []string{},
			ReceivedByTopicCESub:      []string{},
			ReceivedByTopicDead:       sentTopicDeadMessages,
			ReceivedByTopicDeadLetter: sentTopicDeadMessages,
			ReceivedByTopicCEBulkSub:  sentTopicCEMessages,
		})
	return subscriberExternalURL
}

func callInitialize(t *testing.T, subscriberAppName, publisherExternalURL string, protocol string) {
	req := callSubscriberMethodRequest{
		ReqID:     "c-" + uuid.New().String(),
		RemoteApp: subscriberAppName,
		Method:    "initialize",
		Protocol:  protocol,
	}
	// only for the empty-json scenario, initialize empty sets in the subscriber app
	reqBytes, _ := json.Marshal(req)
	_, code, err := utils.HTTPPostWithStatus(publisherExternalURL+"/tests/callSubscriberMethod", reqBytes)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
}

func setDesiredResponse(t *testing.T, subscriberAppName, subscriberResponse, publisherExternalURL, protocol string) {
	// set to respond with specified subscriber response
	req := callSubscriberMethodRequest{
		ReqID:     "c-" + uuid.New().String(),
		RemoteApp: subscriberAppName,
		Method:    "set-respond-" + subscriberResponse,
		Protocol:  protocol,
	}
	reqBytes, _ := json.Marshal(req)
	_, code, err := utils.HTTPPostWithStatus(publisherExternalURL+"/tests/callSubscriberMethod", reqBytes)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
}

func validateMessagesReceivedWhenSomeTopicsBulkSubscribed(t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, validateDeadLetter bool, sentMessages receivedBulkMessagesResponse) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by bulk subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberApp,
		Protocol:  protocol,
		Method:    "getMessages",
	}

	var appResp receivedBulkMessagesResponse
	var err error
	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		request.ReqID = "c-" + uuid.New().String()
		rawReq, _ := json.Marshal(request)
		var resp []byte
		start := time.Now()
		resp, err = utils.HTTPPost(url, rawReq)
		log.Printf("(reqID=%s) Attempt %d complete; took %s", request.ReqID, retryCount, utils.FormatDuration(time.Now().Sub(start)))
		if err != nil {
			log.Printf("(reqID=%s) Error in response: %v", request.ReqID, err)
			time.Sleep(10 * time.Second)
			continue
		}

		err = json.Unmarshal(resp, &appResp)
		if err != nil {
			err = fmt.Errorf("(reqID=%s) failed to unmarshal JSON. Error: %v. Raw data: %s", request.ReqID, err, string(resp))
			log.Printf("Error in response: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		log.Printf(
			"subscriber received %d/%d on raw sub topic and %d/%d on ce sub topic and %d/%d on bulk raw sub topic and %d/%d on bulk ce sub topic ",
			len(appResp.ReceivedByTopicRawSub), len(sentMessages.ReceivedByTopicRawSub),
			len(appResp.ReceivedByTopicCESub), len(sentMessages.ReceivedByTopicCESub),
			len(appResp.ReceivedByTopicRawBulkSub), len(sentMessages.ReceivedByTopicRawBulkSub),
			len(appResp.ReceivedByTopicCEBulkSub), len(sentMessages.ReceivedByTopicCEBulkSub),
		)

		if len(appResp.ReceivedByTopicRawSub) != len(sentMessages.ReceivedByTopicRawSub) ||
			len(appResp.ReceivedByTopicCESub) != len(sentMessages.ReceivedByTopicCESub) ||
			len(appResp.ReceivedByTopicRawBulkSub) != len(sentMessages.ReceivedByTopicRawBulkSub) ||
			len(appResp.ReceivedByTopicCEBulkSub) != len(sentMessages.ReceivedByTopicCEBulkSub) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(10 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	sort.Strings(sentMessages.ReceivedByTopicRawSub)
	sort.Strings(appResp.ReceivedByTopicRawSub)
	sort.Strings(sentMessages.ReceivedByTopicCESub)
	sort.Strings(appResp.ReceivedByTopicCESub)
	sort.Strings(sentMessages.ReceivedByTopicRawBulkSub)
	sort.Strings(appResp.ReceivedByTopicRawBulkSub)
	sort.Strings(sentMessages.ReceivedByTopicCEBulkSub)
	sort.Strings(appResp.ReceivedByTopicCEBulkSub)

	assert.Equal(t, sentMessages.ReceivedByTopicRawSub, appResp.ReceivedByTopicRawSub, "different messages received in Topic Raw Sub")
	assert.Equal(t, sentMessages.ReceivedByTopicCESub, appResp.ReceivedByTopicCESub, "different messages received in Topic CE Sub")
	assert.Equal(t, sentMessages.ReceivedByTopicRawBulkSub, appResp.ReceivedByTopicRawBulkSub, "different messages received in Topic Raw Bulk Sub")
	assert.Equal(t, sentMessages.ReceivedByTopicCEBulkSub, appResp.ReceivedByTopicCEBulkSub, "different messages received in Topic CE Bulk Sub")
}

var apps []struct {
	suite      string
	publisher  string
	subscriber string
} = []struct {
	suite      string
	publisher  string
	subscriber string
}{
	{
		suite:      "built-in-bulk",
		publisher:  publisherAppName,
		subscriber: bulkSubscriberAppName,
	},
}

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:          publisherAppName,
			DaprEnabled:      true,
			ImageName:        "e2e-pubsub-publisher",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "200Mi",
			AppMemoryRequest: "100Mi",
		},
		{
			AppName:          bulkSubscriberAppName,
			DaprEnabled:      true,
			ImageName:        "e2e-pubsub-bulk-subscriber",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "200Mi",
			AppMemoryRequest: "100Mi",
		},
	}
	log.Printf("Creating TestRunner")
	tr = runner.NewTestRunner("pubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner")
	os.Exit(tr.Start(m))
}

var pubsubTests = []struct {
	name               string
	handler            func(*testing.T, string, string, string, string, string) string
	subscriberResponse string
}{
	{
		name:    "publish and bulk subscribe messages successfully",
		handler: testPublishBulkSubscribeSuccessfully,
	}, {
		name:    "drop message will be published to dlq if configured",
		handler: testDropToDeadLetter,
	},
}

func TestBulkPubSubHTTP(t *testing.T) {
	for _, app := range apps {
		t.Log("Enter TestBulkPubSubHTTP")
		publisherExternalURL := tr.Platform.AcquireAppExternalURL(app.publisher)
		require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

		subscriberExternalURL := tr.Platform.AcquireAppExternalURL(app.subscriber)
		require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURLHTTP must not be empty!")

		// This initial probe makes the test wait a little bit longer when needed,
		// making this test less flaky due to delays in the deployment.
		err := utils.HealthCheckApps(publisherExternalURL, subscriberExternalURL)
		require.NoError(t, err)

		err = publishHealthCheck(publisherExternalURL)
		require.NoError(t, err)

		protocol := "http"
		//nolint: gosec
		offset = rand.Intn(randomOffsetMax) + 1
		log.Printf("initial %s offset: %d", app.suite, offset)
		for _, tc := range pubsubTests {
			t.Run(fmt.Sprintf("%s_%s_%s", app.suite, tc.name, protocol), func(t *testing.T) {
				subscriberExternalURL = tc.handler(t, publisherExternalURL, subscriberExternalURL, tc.subscriberResponse, app.subscriber, protocol)
			})
		}
	}
}
