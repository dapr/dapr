//go:build e2e
// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsubapp

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.uber.org/ratelimit"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60

	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 99
	numberOfMessagesToPublish = 100
	publishRateLimitRPS       = 25

	receiveMessageRetries = 10

	publisherAppName  = "pubsub-publisher-grpc"
	subscriberAppName = "pubsub-subscriber-grpc"
)

// sent to the publisher app, which will publish data to dapr.
type publishCommand struct {
	ContentType string            `json:"contentType"`
	Topic       string            `json:"topic"`
	Data        interface{}       `json:"data"`
	Protocol    string            `json:"protocol"`
	Metadata    map[string]string `json:"metadata"`
}

type callSubscriberMethodRequest struct {
	RemoteApp string `json:"remoteApp"`
	Protocol  string `json:"protocol"`
	Method    string `json:"method"`
}

// data returned from the subscriber app.
type receivedMessagesResponse struct {
	ReceivedByTopicA   []string `json:"pubsub-a-topic"`
	ReceivedByTopicB   []string `json:"pubsub-b-topic"`
	ReceivedByTopicC   []string `json:"pubsub-c-topic"`
	ReceivedByTopicRaw []string `json:"pubsub-raw-topic"`
}

type cloudEvent struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	DataContentType string      `json:"datacontenttype"`
	Data            interface{} `json:"data"`
}

// checks is publishing is working.
func publishHealthCheck(publisherExternalURL string) error {
	commandBody := publishCommand{
		ContentType: "application/json",
		Topic:       "pubsub-healthcheck-topic-grpc",
		Protocol:    "grpc",
		Data:        "health check",
	}
	jsonValue, _ := json.Marshal(commandBody)

	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

	return backoff.Retry(func() error {
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
	//nolint: gosec
	offset := rand.Intn(randomOffsetMax)
	for i := offset; i < offset+numberOfMessagesToPublish; i++ {
		// create and marshal message
		messageID := fmt.Sprintf("message-%s-%03d", protocol, i)
		var messageData interface{} = messageID
		if cloudEventType != "" {
			messageData = &cloudEvent{
				ID:              messageID,
				Type:            cloudEventType,
				DataContentType: "text/plain",
				Data:            messageID,
			}
		}
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

func callInitialize(t *testing.T, publisherExternalURL string, protocol string) {
	req := callSubscriberMethodRequest{
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

func testPublish(t *testing.T, publisherExternalURL string, protocol string) receivedMessagesResponse {
	var err error
	sentTopicAMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-a-topic", protocol, nil, "")
	require.NoError(t, err)

	sentTopicBMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-b-topic", protocol, nil, "")
	require.NoError(t, err)

	sentTopicCMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-c-topic", protocol, nil, "")
	require.NoError(t, err)

	metadata := map[string]string{
		"rawPayload": "true",
	}
	sentTopicRawMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-raw-topic", protocol, metadata, "")
	require.NoError(t, err)

	return receivedMessagesResponse{
		ReceivedByTopicA:   sentTopicAMessages,
		ReceivedByTopicB:   sentTopicBMessages,
		ReceivedByTopicC:   sentTopicCMessages,
		ReceivedByTopicRaw: sentTopicRawMessages,
	}
}

func postSingleMessage(url string, data []byte) (int, error) {
	// HTTPPostWithStatus by default sends with content-type application/json
	_, statusCode, err := utils.HTTPPostWithStatus(url, data)
	if err != nil {
		log.Printf("Publish failed with error=%s, response is nil", err.Error())
		return http.StatusInternalServerError, err
	}
	if (statusCode != http.StatusOK) && (statusCode != http.StatusNoContent) {
		err = fmt.Errorf("publish failed with StatusCode=%d", statusCode)
	}
	return statusCode, err
}

func testPublishSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, subscriberAppName, protocol string) string {
	log.Printf("Test publish subscribe success flow\n")
	sentMessages := testPublish(t, publisherExternalURL, protocol)

	validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishWithoutTopic(t *testing.T, publisherExternalURL, subscriberExternalURL, _, _, protocol string) string {
	log.Printf("Test publish without topic\n")
	commandBody := publishCommand{
		Protocol: protocol,
	}
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

//nolint:staticcheck
func testValidateRedeliveryOrEmptyJSON(t *testing.T, publisherExternalURL, subscriberExternalURL, subscriberResponse, subscriberAppName, protocol string) string {
	log.Printf("Validating publisher health...\n")
	_, err := utils.HTTPGetNTimes(publisherExternalURL, numHealthChecks)
	require.NoError(t, err)

	log.Printf("Set subscriber to respond with %s\n", subscriberResponse)
	if subscriberResponse == "empty-json" {
		log.Println("Initialize the sets again in the subscriber application for this scenario ...")
		callInitialize(t, publisherExternalURL, protocol)
	}

	// set to respond with specified subscriber response
	req := callSubscriberMethodRequest{
		RemoteApp: subscriberAppName,
		Method:    "set-respond-" + subscriberResponse,
		Protocol:  protocol,
	}
	reqBytes, _ := json.Marshal(req)
	var lastRetryError error
	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		if retryCount > 0 {
			time.Sleep(10 * time.Second)
		}
		lastRetryError = nil
		_, code, err := utils.HTTPPostWithStatus(publisherExternalURL+"/tests/callSubscriberMethod", reqBytes)
		if err != nil {
			lastRetryError = err
			continue
		}
		if code != http.StatusOK {
			lastRetryError = fmt.Errorf("unexpected http code: %v", code)
			continue
		}

		break
	}
	require.Nil(t, lastRetryError, "error calling /tests/callSubscriberMethod: %v", lastRetryError)
	sentMessages := testPublish(t, publisherExternalURL, protocol)

	if subscriberResponse == "empty-json" {
		// on empty-json response case immediately validate the received messages
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	}

	// restart application
	log.Printf("Restarting subscriber application to check redelivery...\n")
	err = tr.Platform.Restart(subscriberAppName)
	require.NoError(t, err, "error restarting subscriber")
	subscriberExternalURL = tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")
	if protocol == "http" {
		_, err = utils.HTTPGetNTimes(subscriberExternalURL, numHealthChecks)
		require.NoError(t, err)
	} else {
		conn, err := grpc.Dial(subscriberExternalURL, grpc.WithInsecure())
		if err != nil {
			log.Printf("Could not connect to app %s: %s", subscriberExternalURL, err.Error())
		}
		defer conn.Close()
	}

	if subscriberResponse == "empty-json" {
		// validate that there is no redelivery of messages
		log.Printf("Validating no redelivered messages...")
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, receivedMessagesResponse{
			// empty string slices
			ReceivedByTopicA:   []string{},
			ReceivedByTopicB:   []string{},
			ReceivedByTopicC:   []string{},
			ReceivedByTopicRaw: []string{},
		})
	} else {
		// validate redelivery of messages
		log.Printf("Validating redelivered messages...")
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	}
	return subscriberExternalURL
}

func validateMessagesReceivedBySubscriber(t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages receivedMessagesResponse) {
	var err error
	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		if retryCount > 0 {
			log.Printf("Retrying due to error: %v", err)
			time.Sleep(10 * time.Second)
		}

		err = validateMessagesReceivedBySubscriberOrError(t, publisherExternalURL, subscriberApp, protocol, sentMessages)
		if err == nil {
			// Success.
			return
		}
	}

	require.NoError(t, err)
}

func validateMessagesReceivedBySubscriberOrError(
	t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages receivedMessagesResponse) error {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberApp,
		Protocol:  protocol,
		Method:    "getMessages",
	}

	rawReq, _ := json.Marshal(request)

	var appResp receivedMessagesResponse
	resp, err := utils.HTTPPost(url, rawReq)
	if err != nil {
		return err
	}

	err = json.Unmarshal(resp, &appResp)
	if err != nil {
		return err
	}

	log.Printf("subscriber received %d messages on pubsub-a-topic, %d on pubsub-b-topic and %d on pubsub-c-topic and %d on pubsub-raw-topic",
		len(appResp.ReceivedByTopicA), len(appResp.ReceivedByTopicB), len(appResp.ReceivedByTopicC), len(appResp.ReceivedByTopicRaw))

	if len(appResp.ReceivedByTopicA) != len(sentMessages.ReceivedByTopicA) ||
		len(appResp.ReceivedByTopicB) != len(sentMessages.ReceivedByTopicB) ||
		len(appResp.ReceivedByTopicC) != len(sentMessages.ReceivedByTopicC) ||
		len(appResp.ReceivedByTopicRaw) != len(sentMessages.ReceivedByTopicRaw) {
		return fmt.Errorf("differing lengths in received vs sent messages")
	}

	// Sort messages first because the delivered messages might not be ordered.
	sort.Strings(sentMessages.ReceivedByTopicA)
	sort.Strings(appResp.ReceivedByTopicA)
	sort.Strings(sentMessages.ReceivedByTopicB)
	sort.Strings(appResp.ReceivedByTopicB)
	sort.Strings(sentMessages.ReceivedByTopicC)
	sort.Strings(appResp.ReceivedByTopicC)
	sort.Strings(sentMessages.ReceivedByTopicRaw)
	sort.Strings(appResp.ReceivedByTopicRaw)

	if !assert.Equal(t, sentMessages.ReceivedByTopicA, appResp.ReceivedByTopicA) {
		return fmt.Errorf("different messages received in Topic A")
	}

	if !assert.Equal(t, sentMessages.ReceivedByTopicB, appResp.ReceivedByTopicB) {
		return fmt.Errorf("different messages received in Topic B")
	}

	if !assert.Equal(t, sentMessages.ReceivedByTopicC, appResp.ReceivedByTopicC) {
		return fmt.Errorf("different messages received in Topic C")
	}

	if !assert.Equal(t, sentMessages.ReceivedByTopicRaw, appResp.ReceivedByTopicRaw) {
		return fmt.Errorf("different messages received in Topic Raw")
	}

	return nil
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
			MetricsEnabled: true,
		},
		{
			AppName:        subscriberAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-subscriber_grpc",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("pubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

var pubsubTests = []struct {
	name               string
	handler            func(*testing.T, string, string, string, string, string) string
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

func TestPubSubGRPC(t *testing.T) {
	log.Println("Enter TestPubSubGRPC")
	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	subscriberExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(publisherExternalURL, numHealthChecks)
	require.NoError(t, err)

	err = publishHealthCheck(publisherExternalURL)
	require.NoError(t, err)

	protocol := "grpc"
	callInitialize(t, publisherExternalURL, protocol)
	for _, tc := range pubsubTests {
		t.Run(fmt.Sprintf("%s_%s", tc.name, protocol), func(t *testing.T) {
			tc.handler(t, publisherExternalURL, subscriberExternalURL, tc.subscriberResponse, subscriberAppName, protocol)
		})
	}
}
