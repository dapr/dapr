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

package pubsubapp

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

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var tr *runner.TestRunner

const (
	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 49
	numberOfMessagesToPublish = 60
	publishRateLimitRPS       = 25
	receiveMessageRetries     = 5

	metadataPrefix    = "metadata."
	publisherAppName  = "pubsub-publisher-grpc"
	subscriberAppName = "pubsub-subscriber-grpc"
	pubsubKafka       = "kafka-messagebus"
	bulkPubsubMetaKey = "bulkPublishPubsubName"
)

var offset int

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
type receivedMessagesResponse struct {
	ReceivedByTopicA       []string `json:"pubsub-a-topic"`
	ReceivedByTopicB       []string `json:"pubsub-b-topic"`
	ReceivedByTopicC       []string `json:"pubsub-c-topic"`
	ReceivedByTopicRaw     []string `json:"pubsub-raw-topic"`
	ReceivedByTopicBulk    []string `json:"pubsub-bulk-topic"`
	ReceivedByTopicRawBulk []string `json:"pubsub-raw-bulk-topic"`
	ReceivedByTopicCEBulk  []string `json:"pubsub-ce-bulk-topic"`
	ReceivedByTopicDefBulk []string `json:"pubsub-def-bulk-topic"`
}

type receivedBulkMessagesResponse struct {
	ReceivedByTopicRawSub     []string `json:"pubsub-raw-sub-topic"`
	ReceivedByTopicCESub      []string `json:"pubsub-ce-sub-topic"`
	ReceivedByTopicRawBulkSub []string `json:"pubsub-raw-bulk-sub-topic"`
	ReceivedByTopicCEBulkSub  []string `json:"pubsub-ce-bulk-sub-topic"`
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

	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/publish", publisherExternalURL)

	return backoff.Retry(func() error {
		commandBody.ReqID = "c-" + uuid.New().String()
		jsonValue, _ := json.Marshal(commandBody)
		_, err := postSingleMessage(url, jsonValue)
		return err
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
}

func setMetadataBulk(url string, reqMeta map[string]string) string {
	qArgs := []string{}
	for k, v := range reqMeta {
		qArg := fmt.Sprintf("%s%s=%s", metadataPrefix, k, v)
		qArgs = append(qArgs, qArg)
	}
	concatenated := strings.Join(qArgs, "&")
	log.Printf("setting query args %s", concatenated)
	return url + "?" + concatenated
}

// sends messages to the publisher app.  The publisher app does the actual publish.
func sendToPublisherBulk(t *testing.T, publisherExternalURL string, topic string, protocol string, reqMetadata map[string]string, cloudEventType string) ([]string, error) {
	var individualMessages []string
	commands := make([]publishCommand, numberOfMessagesToPublish)
	for i := 0; i < numberOfMessagesToPublish; i++ {
		contentType := "text/plain"
		if cloudEventType != "" {
			contentType = "application/cloudevents+json"
		}
		commandBody := publishCommand{
			ContentType: contentType,
			Topic:       fmt.Sprintf("%s-%s", topic, protocol),
			Protocol:    protocol,
		}

		// create and marshal command
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
		commands[i] = commandBody

		individualMessages = append(individualMessages, messageID)
	}

	jsonValue, _ := json.Marshal(commands)

	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/bulkpublish", publisherExternalURL)

	url = setMetadataBulk(url, reqMetadata)

	// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
	log.Printf("Sending bulk publish, app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)

	statusCode, err := postSingleMessage(url, jsonValue)
	// return on an unsuccessful publish
	if statusCode != http.StatusOK {
		return nil, err
	}

	// return successfully sent individual messages
	return individualMessages, nil
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

func callInitialize(t *testing.T, publisherExternalURL string, protocol string) {
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

func testPublishBulk(t *testing.T, publisherExternalURL string, protocol string) receivedMessagesResponse {
	meta := map[string]string{
		bulkPubsubMetaKey: pubsubKafka,
	}
	sentTopicBulkMessages, err := sendToPublisherBulk(t, publisherExternalURL, "pubsub-bulk-topic", protocol, meta, "")
	require.NoError(t, err)

	sentTopicBulkCEMessages, err := sendToPublisherBulk(t, publisherExternalURL, "pubsub-ce-bulk-topic", protocol, meta, "myevent.CE")
	require.NoError(t, err)

	meta = map[string]string{
		bulkPubsubMetaKey: pubsubKafka,
		"rawPayload":      "true",
	}
	sentTopicBulkRawMessages, err := sendToPublisherBulk(t, publisherExternalURL, "pubsub-raw-bulk-topic", protocol, meta, "")
	require.NoError(t, err)

	sentTopicBulkDefMessages, err := sendToPublisherBulk(t, publisherExternalURL, "pubsub-def-bulk-topic", protocol, nil, "")
	require.NoError(t, err)

	return receivedMessagesResponse{
		ReceivedByTopicBulk:    sentTopicBulkMessages,
		ReceivedByTopicRawBulk: sentTopicBulkRawMessages,
		ReceivedByTopicCEBulk:  sentTopicBulkCEMessages,
		ReceivedByTopicDefBulk: sentTopicBulkDefMessages,
	}
}

func testPublish(t *testing.T, publisherExternalURL string, protocol string) receivedMessagesResponse {
	metadataContentLengthConflict := map[string]string{
		"content-length": "9999999",
	}
	sentTopicAMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-a-topic", protocol, metadataContentLengthConflict, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	sentTopicBMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-b-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	sentTopicCMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-c-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	metadataRawPayload := map[string]string{
		"rawPayload": "true",
	}
	sentTopicRawMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-raw-topic", protocol, metadataRawPayload, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	return receivedMessagesResponse{
		ReceivedByTopicA:   sentTopicAMessages,
		ReceivedByTopicB:   sentTopicBMessages,
		ReceivedByTopicC:   sentTopicCMessages,
		ReceivedByTopicRaw: sentTopicRawMessages,
	}
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

func testBulkPublishSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, subscriberAppName, protocol string) string {
	// set to respond with success
	setDesiredResponse(t, subscriberAppName, "success", publisherExternalURL, protocol)

	log.Printf("Test bulkPublish and normal subscribe success flow")
	sentMessages := testPublishBulk(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateBulkMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, subscriberAppName, protocol string) string {
	log.Printf("Test publish subscribe success flow")
	sentMessages := testPublish(t, publisherExternalURL, protocol)

	validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishBulkSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, _, subscriberAppName, protocol string) string {
	callInitialize(t, publisherExternalURL, protocol)

	log.Printf("Test publish bulk subscribe success flow\n")
	sentMessages := testPublishForBulkSubscribe(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateMessagesReceivedWhenSomeTopicsBulkSubscribed(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishWithoutTopic(t *testing.T, publisherExternalURL, subscriberExternalURL, _, _, protocol string) string {
	log.Printf("Test publish without topic")
	commandBody := publishCommand{
		ReqID:    "c-" + uuid.New().String(),
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
	var err error
	var code int
	log.Print("Validating publisher health...")
	err = utils.HealthCheckApps(publisherExternalURL)
	require.NoError(t, err, "Health checks failed")

	log.Printf("Set subscriber to respond with %s\n", subscriberResponse)
	if subscriberResponse == "empty-json" {
		log.Println("Initialize the sets again in the subscriber application for this scenario ...")
		callInitialize(t, publisherExternalURL, protocol)
	}

	// set to respond with specified subscriber response
	req := callSubscriberMethodRequest{
		ReqID:     "c-" + uuid.New().String(),
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
		_, code, err = utils.HTTPPostWithStatus(publisherExternalURL+"/tests/callSubscriberMethod", reqBytes)
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
		err = utils.HealthCheckApps(subscriberExternalURL)
		require.NoError(t, err, "Health checks failed")
	} else {
		conn, err := grpc.Dial(subscriberExternalURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func setDesiredResponse(t *testing.T, subscriberAppName, subscriberResponse, publisherExternalURL, protocol string) {
	// set to respond with specified subscriber response
	req := callSubscriberMethodRequest{
		ReqID:     "c-" + uuid.New().String(),
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
}

func validateBulkMessagesReceivedBySubscriber(t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages receivedMessagesResponse) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberApp,
		Protocol:  protocol,
		Method:    "getMessages",
	}

	var appResp receivedMessagesResponse
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
			"subscriber received %d/%d messages on pubsub-bulk-topic, %d/%d messages on pubsub-raw-bulk-topic "+
				", %d/%d messages on pubsub-ce-bulk-topic and %d/%d message on pubsub-def-bulk-topic",
			len(appResp.ReceivedByTopicBulk), len(sentMessages.ReceivedByTopicBulk),
			len(appResp.ReceivedByTopicRawBulk), len(sentMessages.ReceivedByTopicRawBulk),
			len(appResp.ReceivedByTopicCEBulk), len(sentMessages.ReceivedByTopicCEBulk),
			len(appResp.ReceivedByTopicDefBulk), len(sentMessages.ReceivedByTopicDefBulk),
		)

		if len(appResp.ReceivedByTopicBulk) != len(sentMessages.ReceivedByTopicBulk) ||
			len(appResp.ReceivedByTopicRawBulk) != len(sentMessages.ReceivedByTopicRawBulk) ||
			len(appResp.ReceivedByTopicCEBulk) != len(sentMessages.ReceivedByTopicCEBulk) ||
			len(appResp.ReceivedByTopicDefBulk) != len(sentMessages.ReceivedByTopicDefBulk) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(10 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	// Sort messages first because the delivered messages cannot be ordered.
	sort.Strings(sentMessages.ReceivedByTopicBulk)
	sort.Strings(appResp.ReceivedByTopicBulk)
	sort.Strings(sentMessages.ReceivedByTopicRawBulk)
	sort.Strings(appResp.ReceivedByTopicRawBulk)
	sort.Strings(sentMessages.ReceivedByTopicCEBulk)
	sort.Strings(appResp.ReceivedByTopicCEBulk)
	sort.Strings(sentMessages.ReceivedByTopicDefBulk)
	sort.Strings(appResp.ReceivedByTopicDefBulk)

	assert.Equal(t, sentMessages.ReceivedByTopicBulk, appResp.ReceivedByTopicBulk, "different messages received in Topic Bulk")
	assert.Equal(t, sentMessages.ReceivedByTopicRawBulk, appResp.ReceivedByTopicRawBulk, "different messages received in Topic Raw Bulk")
	assert.Equal(t, sentMessages.ReceivedByTopicCEBulk, appResp.ReceivedByTopicCEBulk, "different messages received in Topic CE Bulk")
	assert.Equal(t, sentMessages.ReceivedByTopicDefBulk, appResp.ReceivedByTopicDefBulk, "different messages received in Topic defult Bulk on redis")
}

func validateMessagesReceivedBySubscriber(
	t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages receivedMessagesResponse,
) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberApp,
		Protocol:  protocol,
		Method:    "getMessages",
	}

	var appResp receivedMessagesResponse
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
			"subscriber received %d/%d messages on pubsub-a-topic, %d/%d on pubsub-b-topic and %d/%d on pubsub-c-topic and %d/%d on pubsub-raw-topic",
			len(appResp.ReceivedByTopicA), len(sentMessages.ReceivedByTopicA),
			len(appResp.ReceivedByTopicB), len(sentMessages.ReceivedByTopicB),
			len(appResp.ReceivedByTopicC), len(sentMessages.ReceivedByTopicC),
			len(appResp.ReceivedByTopicRaw), len(sentMessages.ReceivedByTopicRaw),
		)

		if len(appResp.ReceivedByTopicA) != len(sentMessages.ReceivedByTopicA) ||
			len(appResp.ReceivedByTopicB) != len(sentMessages.ReceivedByTopicB) ||
			len(appResp.ReceivedByTopicC) != len(sentMessages.ReceivedByTopicC) ||
			len(appResp.ReceivedByTopicRaw) != len(sentMessages.ReceivedByTopicRaw) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	// Sort messages first because the delivered messages might not be ordered.
	sort.Strings(sentMessages.ReceivedByTopicA)
	sort.Strings(appResp.ReceivedByTopicA)
	sort.Strings(sentMessages.ReceivedByTopicB)
	sort.Strings(appResp.ReceivedByTopicB)
	sort.Strings(sentMessages.ReceivedByTopicC)
	sort.Strings(appResp.ReceivedByTopicC)
	sort.Strings(sentMessages.ReceivedByTopicRaw)
	sort.Strings(appResp.ReceivedByTopicRaw)

	assert.Equal(t, sentMessages.ReceivedByTopicA, appResp.ReceivedByTopicA, "different messages received in Topic A")
	assert.Equal(t, sentMessages.ReceivedByTopicB, appResp.ReceivedByTopicB, "different messages received in Topic B")
	assert.Equal(t, sentMessages.ReceivedByTopicC, appResp.ReceivedByTopicC, "different messages received in Topic C")
	assert.Equal(t, sentMessages.ReceivedByTopicRaw, appResp.ReceivedByTopicRaw, "different messages received in Topic Raw")
}

func validateMessagesReceivedWhenSomeTopicsBulkSubscribed(
	t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages receivedBulkMessagesResponse,
) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

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
			"subscriber received %d/%d on raw sub topic and %d/%d on ce sub topic and %d/%d on bulk raw sub topic and %d/%d on bulk ce sub topic",
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
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	// Sort messages first because the delivered messages might not be ordered.
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

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_grpc")
	utils.InitHTTPClient(true)

	//nolint: gosec
	offset = rand.Intn(randomOffsetMax) + 1
	log.Printf("initial offset: %d", offset)

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
		name:    "publish and bulk subscribe messages successfully",
		handler: testPublishBulkSubscribeSuccessfully,
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
	{
		name:    "bulk publish and normal subscribe successfully",
		handler: testBulkPublishSuccessfully,
	},
}

func TestPubSubGRPC(t *testing.T) {
	log.Println("Enter TestPubSubGRPC")
	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	subscriberExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")

	// Makes the test wait for the apps and load balancers to be ready
	err := utils.HealthCheckApps(publisherExternalURL)
	require.NoError(t, err, "Health checks failed")

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
