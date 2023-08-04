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

	publisherAppName  = "pubsub-publisher-routing-grpc"
	subscriberAppName = "pubsub-subscriber-routing-grpc"
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

// data returned from the subscriber-routing_grpc app.
type routedMessagesResponse struct {
	RouteA []string `json:"route-a"`
	RouteB []string `json:"route-b"`
	RouteC []string `json:"route-c"`
	RouteD []string `json:"route-d"`
	RouteE []string `json:"route-e"`
	RouteF []string `json:"route-f"`
}

type cloudEvent struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	DataContentType string      `json:"datacontenttype"`
	Data            interface{} `json:"data"`
}

// sends messages to the publisher app.  The publisher app does the actual publish.
func sendToPublisher(t *testing.T, offset int, publisherExternalURL string, topic string, protocol string, metadata map[string]string, cloudEventType string) ([]string, error) {
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

func postSingleMessage(url string, data []byte) (int, error) {
	// HTTPPostWithStatus by default sends with content-type application/json
	start := time.Now()
	_, statusCode, err := utils.HTTPPostWithStatus(url, data)
	if err != nil {
		log.Printf("Publish failed with error=%s (body=%s) (duration=%s)", err.Error(), data, utils.FormatDuration(time.Now().Sub(start)))
		return http.StatusInternalServerError, err
	}
	if statusCode != http.StatusOK {
		err = fmt.Errorf("publish failed with StatusCode=%d (body=%s) (duration=%s)", statusCode, data, utils.FormatDuration(time.Now().Sub(start)))
	}
	return statusCode, err
}

func testPublishSubscribeRouting(t *testing.T, publisherExternalURL, subscriberExternalURL, subscriberAppName, protocol string) string {
	log.Printf("Test publish subscribe routing flow\n")
	callInitialize(t, publisherExternalURL, protocol)
	sentMessages := testPublishRouting(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateMessagesRouted(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishRouting(t *testing.T, publisherExternalURL string, protocol string) routedMessagesResponse {
	//nolint: gosec
	offset := rand.Intn(randomOffsetMax) + 1
	log.Printf("initial offset: %d", offset)
	// set to respond with success
	sentRouteAMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.A")
	require.NoError(t, err)

	offset += numberOfMessagesToPublish + 1
	sentRouteBMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.B")
	require.NoError(t, err)

	offset += numberOfMessagesToPublish + 1
	sentRouteCMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.C")
	require.NoError(t, err)

	offset += numberOfMessagesToPublish + 1
	sentRouteDMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.D")
	require.NoError(t, err)

	offset += numberOfMessagesToPublish + 1
	sentRouteEMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.E")
	require.NoError(t, err)

	offset += numberOfMessagesToPublish + 1
	sentRouteFMessages, err := sendToPublisher(t, offset, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.F")
	require.NoError(t, err)

	return routedMessagesResponse{
		RouteA: sentRouteAMessages,
		RouteB: sentRouteBMessages,
		RouteC: sentRouteCMessages,
		RouteD: sentRouteDMessages,
		RouteE: sentRouteEMessages,
		RouteF: sentRouteFMessages,
	}
}

func validateMessagesRouted(t *testing.T, publisherExternalURL string, subscriberApp string, protocol string, sentMessages routedMessagesResponse) {
	// this is the subscribe app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/callSubscriberMethod", publisherExternalURL)
	log.Printf("Getting messages received by subscriber using url %s", url)

	request := callSubscriberMethodRequest{
		RemoteApp: subscriberApp,
		Protocol:  protocol,
		Method:    "getMessages",
	}

	var appResp routedMessagesResponse
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
			"subscriber received messages: route-a %d/%d, route-b %d/%d, route-c %d/%d, route-d %d/%d, route-e %d/%d, route-f %d/%d",
			len(appResp.RouteA), len(sentMessages.RouteA),
			len(appResp.RouteB), len(sentMessages.RouteB),
			len(appResp.RouteC), len(sentMessages.RouteC),
			len(appResp.RouteD), len(sentMessages.RouteD),
			len(appResp.RouteE), len(sentMessages.RouteE),
			len(appResp.RouteF), len(sentMessages.RouteF),
		)

		if len(appResp.RouteA) != len(sentMessages.RouteA) ||
			len(appResp.RouteB) != len(sentMessages.RouteB) ||
			len(appResp.RouteC) != len(sentMessages.RouteC) ||
			len(appResp.RouteD) != len(sentMessages.RouteD) ||
			len(appResp.RouteE) != len(sentMessages.RouteE) ||
			len(appResp.RouteF) != len(sentMessages.RouteF) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	require.NoError(t, err, "too many failed attempts")

	// Sort messages first because the delivered messages cannot be ordered.
	sort.Strings(sentMessages.RouteA)
	sort.Strings(appResp.RouteA)
	sort.Strings(sentMessages.RouteB)
	sort.Strings(appResp.RouteB)
	sort.Strings(sentMessages.RouteC)
	sort.Strings(appResp.RouteC)
	sort.Strings(sentMessages.RouteD)
	sort.Strings(appResp.RouteD)
	sort.Strings(sentMessages.RouteE)
	sort.Strings(appResp.RouteE)
	sort.Strings(sentMessages.RouteF)
	sort.Strings(appResp.RouteF)

	assert.Equal(t, sentMessages.RouteA, appResp.RouteA, "different messages received in route A")
	assert.Equal(t, sentMessages.RouteB, appResp.RouteB, "different messages received in route B")
	assert.Equal(t, sentMessages.RouteC, appResp.RouteC, "different messages received in route C")
	assert.Equal(t, sentMessages.RouteD, appResp.RouteD, "different messages received in route D")
	assert.Equal(t, sentMessages.RouteE, appResp.RouteE, "different messages received in route E")
	assert.Equal(t, sentMessages.RouteF, appResp.RouteF, "different messages received in route F")
}

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_routing_grpc")
	utils.InitHTTPClient(true)

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
			ImageName:      "e2e-pubsub-subscriber-routing_grpc",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	log.Printf("Creating TestRunner")
	tr = runner.NewTestRunner("pubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner")
	os.Exit(tr.Start(m))
}

func TestPubSubGRPCRouting(t *testing.T) {
	t.Log("Enter TestPubSubGRPCRouting")

	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	subscriberRoutingExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberRoutingExternalURL, "subscriberRoutingExternalURL must not be empty!")

	// Makes the test wait for the apps and load balancers to be ready
	err := utils.HealthCheckApps(publisherExternalURL)
	require.NoError(t, err, "Health checks failed")

	testPublishSubscribeRouting(t, publisherExternalURL, subscriberRoutingExternalURL, subscriberAppName, "grpc")
}
