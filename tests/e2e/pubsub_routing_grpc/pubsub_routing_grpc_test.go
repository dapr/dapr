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

	receiveMessageRetries = 5

	publisherAppName  = "pubsub-publisher-routing-grpc"
	subscriberAppName = "pubsub-subscriber-routing-grpc"
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

func testPublishSubscribeRouting(t *testing.T, publisherExternalURL, subscriberExternalURL, subscriberAppName, protocol string) string {
	log.Printf("Test publish subscribe routing flow\n")
	callInitialize(t, publisherExternalURL, protocol)
	sentMessages := testPublishRouting(t, publisherExternalURL, protocol)

	time.Sleep(5 * time.Second)
	validateMessagesRouted(t, publisherExternalURL, subscriberAppName, protocol, sentMessages)
	return subscriberExternalURL
}

func testPublishRouting(t *testing.T, publisherExternalURL string, protocol string) routedMessagesResponse {
	// set to respond with success
	sentRouteAMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.A")
	require.NoError(t, err)

	sentRouteBMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.B")
	require.NoError(t, err)

	sentRouteCMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing", protocol, nil, "myevent.C")
	require.NoError(t, err)

	sentRouteDMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.D")
	require.NoError(t, err)

	sentRouteEMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.E")
	require.NoError(t, err)

	sentRouteFMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-routing-crd", protocol, nil, "myevent.F")
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

	rawReq, _ := json.Marshal(request)

	var appResp routedMessagesResponse
	for retryCount := 0; retryCount < receiveMessageRetries; retryCount++ {
		resp, err := utils.HTTPPost(url, rawReq)
		require.NoError(t, err)

		err = json.Unmarshal(resp, &appResp)
		require.NoError(t, err)

		log.Printf("subscriber received message: route-a %d, route-b %d, route-c %d, route-d %d, route-e %d, route-f %d",
			len(appResp.RouteA), len(appResp.RouteB), len(appResp.RouteC),
			len(appResp.RouteD), len(appResp.RouteE), len(appResp.RouteF))

		if len(appResp.RouteA) != len(sentMessages.RouteA) ||
			len(appResp.RouteB) != len(sentMessages.RouteB) ||
			len(appResp.RouteC) != len(sentMessages.RouteC) ||
			len(appResp.RouteD) != len(sentMessages.RouteD) ||
			len(appResp.RouteE) != len(sentMessages.RouteE) ||
			len(appResp.RouteF) != len(sentMessages.RouteF) {
			log.Printf("Differing lengths in received vs. sent messages, retrying.")
			time.Sleep(1 * time.Second)
		} else {
			break
		}
	}

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

	require.Equal(t, sentMessages.RouteA, appResp.RouteA)
	require.Equal(t, sentMessages.RouteB, appResp.RouteB)
	require.Equal(t, sentMessages.RouteC, appResp.RouteC)
	require.Equal(t, sentMessages.RouteD, appResp.RouteD)
	require.Equal(t, sentMessages.RouteE, appResp.RouteE)
	require.Equal(t, sentMessages.RouteF, appResp.RouteF)
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
			ImageName:      "e2e-pubsub-subscriber-routing_grpc",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
			Config:         "pubsubroutingconfig",
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("pubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestPubSubGRPCRouting(t *testing.T) {
	t.Log("Enter TestPubSubGRPCRouting")

	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	subscriberRoutingExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberRoutingExternalURL, "subscriberRoutingExternalURL must not be empty!")

	_, err := utils.HTTPGetNTimes(publisherExternalURL, numHealthChecks)
	require.NoError(t, err)

	testPublishSubscribeRouting(t, publisherExternalURL, subscriberRoutingExternalURL, subscriberAppName, "grpc")
}
