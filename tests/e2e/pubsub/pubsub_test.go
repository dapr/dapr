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

package pubsubapp_e2e

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

	apiv1 "k8s.io/api/core/v1"
)

var tr *runner.TestRunner

const (
	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 49
	numberOfMessagesToPublish = 60
	publishRateLimitRPS       = 25

	receiveMessageRetries = 5

	metadataPrefix             = "metadata."
	publisherAppName           = "pubsub-publisher"
	subscriberAppName          = "pubsub-subscriber"
	publisherPluggableAppName  = "pubsub-publisher-pluggable"
	subscriberPluggableAppName = "pubsub-subscriber-pluggable"
	redisPubSubPluggableApp    = "e2e-pluggable_redis-pubsub"
	PubSubEnvVar               = "DAPR_TEST_PUBSUB_NAME"
	PubSubPluggableName        = "pluggable-messagebus"
	pubsubKafka                = "kafka-messagebus"
	bulkPubsubMetaKey          = "bulkPublishPubsubName"
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
type receivedMessagesResponse struct {
	ReceivedByTopicA          []string `json:"pubsub-a-topic"`
	ReceivedByTopicB          []string `json:"pubsub-b-topic"`
	ReceivedByTopicC          []string `json:"pubsub-c-topic"`
	ReceivedByTopicBulk       []string `json:"pubsub-bulk-topic"`
	ReceivedByTopicRawBulk    []string `json:"pubsub-raw-bulk-topic"`
	ReceivedByTopicCEBulk     []string `json:"pubsub-ce-bulk-topic"`
	ReceivedByTopicDefBulk    []string `json:"pubsub-def-bulk-topic"`
	ReceivedByTopicRaw        []string `json:"pubsub-raw-topic"`
	ReceivedByTopicDead       []string `json:"pubsub-dead-topic"`
	ReceivedByTopicDeadLetter []string `json:"pubsub-deadletter-topic"`
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

	jsonValue, err := json.Marshal(commands)

	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/bulkpublish", publisherExternalURL)

	url = setMetadataBulk(url, reqMetadata)

	// debuggability - trace info about the first message.  don't trace others so it doesn't flood log.
	log.Printf("Sending bulk publish, app at url %s and body '%s', this log will not print for subsequent messages for same topic", url, jsonValue)

	statusCode, err := postSingleMessage(url, jsonValue)
	// return on an unsuccessful publish
	if statusCode != http.StatusNoContent {
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

	// empty pubsub will default to existing pubsub via redis
	sentTopicBulkDefMessages, err := sendToPublisherBulk(t, publisherExternalURL, "pubsub-def-bulk-topic", protocol, nil, "myevent.CE")
	require.NoError(t, err)

	return receivedMessagesResponse{
		ReceivedByTopicBulk:    sentTopicBulkMessages,
		ReceivedByTopicRawBulk: sentTopicBulkRawMessages,
		ReceivedByTopicCEBulk:  sentTopicBulkCEMessages,
		ReceivedByTopicDefBulk: sentTopicBulkDefMessages,
	}
}

func testPublish(t *testing.T, publisherExternalURL string, protocol string) receivedMessagesResponse {
	sentTopicDeadMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-dead-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	// Test bug where content-length metadata conflict makes message undeliverable in grpc subscriber.
	// We set an arbitrarily large number that it is unlikely to match the size of the payload daprd delivers.
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
		ReceivedByTopicA:          sentTopicAMessages,
		ReceivedByTopicB:          sentTopicBMessages,
		ReceivedByTopicC:          sentTopicCMessages,
		ReceivedByTopicRaw:        sentTopicRawMessages,
		ReceivedByTopicDead:       sentTopicDeadMessages,
		ReceivedByTopicDeadLetter: sentTopicDeadMessages,
	}
}

func testDropToDeadLetter(t *testing.T, publisherExternalURL, subscriberExternalURL, _, subscriberAppName, protocol string) string {
	setDesiredResponse(t, subscriberAppName, "drop", publisherExternalURL, protocol)
	callInitialize(t, subscriberAppName, publisherExternalURL, protocol)

	sentTopicDeadMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-dead-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	sentTopicNormal, err := sendToPublisher(t, publisherExternalURL, "pubsub-a-topic", protocol, nil, "")
	require.NoError(t, err)
	offset += numberOfMessagesToPublish + 1

	time.Sleep(5 * time.Second)
	received := receivedMessagesResponse{
		ReceivedByTopicA:          sentTopicNormal,
		ReceivedByTopicB:          []string{},
		ReceivedByTopicC:          []string{},
		ReceivedByTopicRaw:        []string{},
		ReceivedByTopicDead:       sentTopicDeadMessages,
		ReceivedByTopicDeadLetter: sentTopicDeadMessages,
	}
	validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, true, received)
	return subscriberExternalURL
}

func postSingleMessage(url string, data []byte) (int, error) {
	// HTTPPostWithStatus by default sends with content-type application/json
	start := time.Now()
	body, statusCode, err := utils.HTTPPostWithStatus(url, data)
	if err != nil {
		log.Printf("Publish failed with error=%s (response=%s) (request=%s) (duration=%s)", err.Error(), string(body), string(data), utils.FormatDuration(time.Since(start)))
		return http.StatusInternalServerError, err
	}
	if (statusCode != http.StatusOK) && (statusCode != http.StatusNoContent) {
		log.Printf("publish failed with StatusCode=%d (response=%s) (request=%s) (duration=%s)", statusCode, string(body), string(data), utils.FormatDuration(time.Since(start)))
		err = fmt.Errorf("publish failed with StatusCode=%d (response=%s) (request=%s) (duration=%s)", statusCode, string(body), string(data), utils.FormatDuration(time.Since(start)))
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
	// set to respond with success
	setDesiredResponse(t, subscriberAppName, "success", publisherExternalURL, protocol)

	log.Print("Test publish subscribe success flow")
	sentMessages := testPublish(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, false, sentMessages)
	return subscriberExternalURL
}

func testPublishWithoutTopic(t *testing.T, publisherExternalURL, subscriberExternalURL, _, _, protocol string) string {
	log.Print("Test publish without topic")
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

func testValidateRedeliveryOrEmptyJSON(t *testing.T, publisherExternalURL, subscriberExternalURL, subscriberResponse, subscriberAppName, protocol string) string {
	log.Printf("Set subscriber to respond with %s\n", subscriberResponse)

	log.Println("Initialize the sets for this scenario ...")
	callInitialize(t, subscriberAppName, publisherExternalURL, protocol)

	// set to respond with specified subscriber response
	setDesiredResponse(t, subscriberAppName, subscriberResponse, publisherExternalURL, protocol)

	sentMessages := testPublish(t, publisherExternalURL, protocol)

	if subscriberResponse == "empty-json" {
		// on empty-json response case immediately validate the received messages
		time.Sleep(10 * time.Second)
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, false, sentMessages)

		callInitialize(t, subscriberAppName, publisherExternalURL, protocol)
	} else {
		// Sleep a few seconds to ensure there's time for all messages to be delivered at least once, so if they have to be sent to the DLQ, they can be before we change the desired response status
		time.Sleep(5 * time.Second)
	}

	// set to respond with success
	setDesiredResponse(t, subscriberAppName, "success", publisherExternalURL, protocol)

	if subscriberResponse == "empty-json" {
		// validate that there is no redelivery of messages
		log.Printf("Validating no redelivered messages...")
		time.Sleep(30 * time.Second)
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, false, receivedMessagesResponse{
			// empty string slices
			ReceivedByTopicA:    []string{},
			ReceivedByTopicB:    []string{},
			ReceivedByTopicC:    []string{},
			ReceivedByTopicRaw:  []string{},
			ReceivedByTopicDead: []string{},
		})
	} else if subscriberResponse == "error" {
		log.Printf("Validating redelivered messages...")
		time.Sleep(30 * time.Second)
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, true, sentMessages)
	} else {
		// validate redelivery of messages
		log.Printf("Validating redelivered messages...")
		time.Sleep(30 * time.Second)
		validateMessagesReceivedBySubscriber(t, publisherExternalURL, subscriberAppName, protocol, false, sentMessages)
	}

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
				", %d/%d messages on pubsub-ce-bulk-topic and %d/%d messages on pubsub-def-bulk-topic",
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
	assert.Equal(t, sentMessages.ReceivedByTopicDefBulk, appResp.ReceivedByTopicDefBulk, "different messages received in Topic Def Bulk impl redis")
}

func validateMessagesReceivedBySubscriber(t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, validateDeadLetter bool, sentMessages receivedMessagesResponse) {
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
			"subscriber received %d/%d messages on pubsub-a-topic, %d/%d on pubsub-b-topic and %d/%d on pubsub-c-topic, %d/%d on pubsub-raw-topic and %d/%d on dead letter topic",
			len(appResp.ReceivedByTopicA), len(sentMessages.ReceivedByTopicA),
			len(appResp.ReceivedByTopicB), len(sentMessages.ReceivedByTopicB),
			len(appResp.ReceivedByTopicC), len(sentMessages.ReceivedByTopicC),
			len(appResp.ReceivedByTopicRaw), len(sentMessages.ReceivedByTopicRaw),
			len(appResp.ReceivedByTopicDeadLetter), len(sentMessages.ReceivedByTopicDeadLetter),
		)

		if len(appResp.ReceivedByTopicA) != len(sentMessages.ReceivedByTopicA) ||
			len(appResp.ReceivedByTopicB) != len(sentMessages.ReceivedByTopicB) ||
			len(appResp.ReceivedByTopicC) != len(sentMessages.ReceivedByTopicC) ||
			len(appResp.ReceivedByTopicRaw) != len(sentMessages.ReceivedByTopicRaw) ||
			(validateDeadLetter && len(appResp.ReceivedByTopicDeadLetter) != len(sentMessages.ReceivedByTopicDeadLetter)) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(10 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	// Sort messages first because the delivered messages cannot be ordered.
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
	if validateDeadLetter {
		// only error response is expected to validate dead letter
		sort.Strings(sentMessages.ReceivedByTopicDeadLetter)
		sort.Strings(appResp.ReceivedByTopicDeadLetter)
		assert.Equal(t, sentMessages.ReceivedByTopicDeadLetter, appResp.ReceivedByTopicDeadLetter, "different messages received in Topic Dead")
	}
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
		suite:      "built-in",
		publisher:  publisherAppName,
		subscriber: subscriberAppName,
	},
}

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub")
	utils.InitHTTPClient(true)

	components := []kube.ComponentDescription{}

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
			AppName:          subscriberAppName,
			DaprEnabled:      true,
			ImageName:        "e2e-pubsub-subscriber",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "200Mi",
			AppMemoryRequest: "100Mi",
		},
	}

	if utils.TestTargetOS() != "windows" { // pluggable components feature requires unix socket to work
		redisPubsubPluggableComponent := apiv1.Container{
			Name:  "redis-pubsub-pluggable",
			Image: runner.BuildTestImageName(redisPubSubPluggableApp),
		}
		container, _ := json.Marshal(redisPubsubPluggableComponent)

		components = append(components, kube.ComponentDescription{
			Name:            PubSubPluggableName,
			Namespace:       &kube.DaprTestNamespace,
			TypeName:        "pubsub.redis-pluggable",
			ContainerAsJSON: string(container),
			MetaData: map[string]kube.MetadataValue{
				"redisHost": {
					FromSecretRef: &kube.SecretRef{
						Name: "redissecret",
						Key:  "host",
					},
				},
				"redisPassword":      {Raw: `""`},
				"processingTimeout":  {Raw: `"1s"`},
				"redeliverInterval":  {Raw: `"1s"`},
				"idleCheckFrequency": {Raw: `"1s"`},
				"readTimeout":        {Raw: `"1s"`},
			},
			Scopes: []string{publisherPluggableAppName, subscriberPluggableAppName},
		})

		pluggableTestApps := []kube.AppDescription{
			{
				AppName:                   publisherPluggableAppName,
				DaprEnabled:               true,
				ImageName:                 "e2e-pubsub-publisher",
				Replicas:                  1,
				IngressEnabled:            true,
				MetricsEnabled:            true,
				AppMemoryLimit:            "200Mi",
				AppMemoryRequest:          "100Mi",
				InjectPluggableComponents: true,
				AppEnv: map[string]string{
					PubSubEnvVar: PubSubPluggableName,
				},
			},
			{
				AppName:                   subscriberPluggableAppName,
				DaprEnabled:               true,
				ImageName:                 "e2e-pubsub-subscriber",
				Replicas:                  1,
				IngressEnabled:            true,
				MetricsEnabled:            true,
				AppMemoryLimit:            "200Mi",
				AppMemoryRequest:          "100Mi",
				InjectPluggableComponents: true,
				AppEnv: map[string]string{
					PubSubEnvVar: PubSubPluggableName,
				},
			},
		}
		testApps = append(testApps, pluggableTestApps...)
		apps = append(apps, struct {
			suite      string
			publisher  string
			subscriber string
		}{
			suite:      "pluggable",
			publisher:  publisherPluggableAppName,
			subscriber: subscriberPluggableAppName,
		})
	}

	log.Printf("Creating TestRunner")
	tr = runner.NewTestRunner("pubsubtest", testApps, components, nil)
	log.Printf("Starting TestRunner")
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
	{
		name:    "bulk publish and normal subscribe successfully",
		handler: testBulkPublishSuccessfully,
	},
	{
		name:    "drop message will be published to dlq if configured",
		handler: testDropToDeadLetter,
	},
}

func TestPubSubHTTP(t *testing.T) {
	for _, app := range apps {
		t.Log("Enter TestPubSubHTTP")
		publisherExternalURL := tr.Platform.AcquireAppExternalURL(app.publisher)
		require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

		subscriberExternalURL := tr.Platform.AcquireAppExternalURL(app.subscriber)
		require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURLHTTP must not be empty!")

		// Makes the test wait for the apps and load balancers to be ready
		err := utils.HealthCheckApps(publisherExternalURL, subscriberExternalURL)
		require.NoError(t, err, "Health checks failed")

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
