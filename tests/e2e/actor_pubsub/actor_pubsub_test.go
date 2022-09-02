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

package actor_pubsub_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

var tr *runner.TestRunner

const (
	numberOfMessagesToPublish = 60
	publishRateLimitRPS       = 25
	defaultActorType          = "actorPubsubTypeE2e"
	firstSubActorType         = "actorTypeTest1Sub"
	SecondSubActorType        = "actorTypeTest2Sub"
	anotherActorID            = "10"
	defaultActorId            = "8"            // Published actor id
	AppName                   = "actor-pubsub" // App name in Dapr.
	numHealthChecks           = 50             // Number of get calls before starting tests.
	actorlogsURLFormat        = "%s/test/logs" // URL to fetch logs from test app.
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

// Cloud event with the actor's information
type cloudEvent struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	DataContentType string `json:"datacontenttype"`
	ActorType       string `json:"actortype"`
	ActorID         string `json:"actorid"`
	Data            string `json:"data"`
}

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

// Checks is publishing is working.
func publishHealthCheck(publisherExternalURL string) error {
	commandBody := publishCommand{
		ContentType: "application/json",
		Topic:       "pubsub-healthcheck-topic-http",
		Protocol:    "http",
		Data:        "health check",
	}

	// this is the actor publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/test/actors/%s/%s/publish", publisherExternalURL, defaultActorType, defaultActorId)

	return backoff.Retry(func() error {
		commandBody.ReqID = "c-" + uuid.New().String()
		jsonValue, _ := json.Marshal(commandBody)
		_, err := postSingleMessage(url, jsonValue)
		return err
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
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
		err = fmt.Errorf("Publish failed with StatusCode=%d (body=%s) (duration=%s)", statusCode, data, utils.FormatDuration(time.Now().Sub(start)))
	}
	return statusCode, err
}

func sendToPublisher(t *testing.T, externalURL string, topic string, protocol string, metadata map[string]string, cloudEventType string) (string, error) {
	var sentMessages string
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

	// Send 5 messages to the topic
	for i := 0; i < 5; i++ {
		// create and marshal message
		messageID := fmt.Sprintf("msg-%s-%s-%04d", strings.TrimSuffix(topic, "-topic"), protocol, i)
		var messageData interface{} = messageID
		if cloudEventType != "" {
			messageData = &cloudEvent{
				ID:              messageID,
				Type:            cloudEventType,
				DataContentType: "text/plain",
				Data:            messageID,
				ActorType:       defaultActorType,
				ActorID:         defaultActorId,
			}
		}
		commandBody.ReqID = "c-" + uuid.New().String()
		commandBody.Data = messageData
		jsonValue, err := json.Marshal(commandBody)
		require.NoError(t, err)

		// this is the publish app's endpoint, not a dapr endpoint
		url := fmt.Sprintf("http://%s/test/actors/%s/%s/publish", externalURL, defaultActorType, defaultActorId)

		rateLimit.Take()
		statusCode, err := postSingleMessage(url, jsonValue)
		// return on an unsuccessful publish
		if statusCode != http.StatusNoContent {
			return "", err
		}
		sentMessages = messageID
	}
	return sentMessages, nil
}

// Sends a message without ActorType
func sendToPublisherNoType(t *testing.T, externalURL string, topic string, protocol string, metadata map[string]string, cloudEventType string) (string, error) {
	var sentMessages string
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

	// Send 5 messages to the topic
	for i := 0; i < 5; i++ {
		// create and marshal message
		messageID := fmt.Sprintf("msg-%s-%s-%04d", strings.TrimSuffix(topic, "-topic"), protocol, i)
		var messageData interface{} = messageID
		if cloudEventType != "" {
			messageData = &cloudEvent{
				ID:              messageID,
				Type:            cloudEventType,
				DataContentType: "text/plain",
				Data:            messageID,
				ActorType:       "",
				ActorID:         defaultActorId,
			}
		}
		commandBody.ReqID = "c-" + uuid.New().String()
		commandBody.Data = messageData
		jsonValue, err := json.Marshal(commandBody)
		require.NoError(t, err)

		// this is the publish app's endpoint
		url := fmt.Sprintf("http://%s/test/actors/%s/%s/publish", externalURL, "NOTACTOR", defaultActorId)

		rateLimit.Take()
		statusCode, err := postSingleMessage(url, jsonValue)
		// return on an unsuccessful publish
		if statusCode != http.StatusNoContent {
			return "", err
		}
		sentMessages = messageID
	}
	return sentMessages, nil
}

// Actors Log control
func parseLogEntries(resp []byte) []actorLogEntry {
	logEntries := []actorLogEntry{}
	err := json.Unmarshal(resp, &logEntries)
	if err != nil {
		return nil
	}

	return logEntries
}

func findActorActivation(resp []byte, actorType string, actorID string) bool {
	return findActorAction(resp, actorType, actorID, "activation")
}

func findActorDeactivation(resp []byte, actorType string, actorID string) bool {
	return findActorAction(resp, actorType, actorID, "deactivation")
}

func findActorMethodInvokation(resp []byte, actorType string, actorID string) bool {
	return findActorAction(resp, actorType, actorID, "actormethod")
}

func findActorAction(resp []byte, actorType string, actorID string, action string) bool {
	logEntries := parseLogEntries(resp)
	for _, logEntry := range logEntries {
		if (logEntry.ActorID == actorID) && (logEntry.Action == action) && (logEntry.ActorType == actorType) {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	utils.SetupLogs("Actorspubsub")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "actorpubsub",
			DaprEnabled:    true,
			ImageName:      "e2e-actorpubsub",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("ActorspPubsubTest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestActorPubSub(t *testing.T) {
	// Get the external url from the app
	actorPubsubExternalURL := tr.Platform.AcquireAppExternalURL("actorpubsub")
	require.NotEmpty(t, actorPubsubExternalURL, "Actor External URL must not be empty!")

	_, err := utils.HTTPGetNTimes(actorPubsubExternalURL, numHealthChecks)
	require.NoError(t, err)

	err = publishHealthCheck(actorPubsubExternalURL)
	require.NoError(t, err)

	// Wait until runtime finds the leader of placements.
	time.Sleep(5 * time.Second)

	// PUBLISH with Http protocol
	protocol := "http"
	t.Run(fmt.Sprintf("%s_%s", "actor-pubsub-publish", protocol), func(t *testing.T) {
		// testPublishSubscribeSuccessfully(t, actorPubsubExternalURL, protocol, "mytopic")
		// Call publish function for the specific topic
		_, err := sendToPublisher(t, actorPubsubExternalURL, "myactorpubsubtopic", protocol, nil, "")
		require.NoError(t, err)

		// Wait for actor to receive the method
		time.Sleep(2 * time.Second)

		// Check actors log for activation
		logsURL := fmt.Sprintf(actorlogsURLFormat, actorPubsubExternalURL)

		fmt.Printf("getting logs, the current time is %s\n", time.Now())
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		require.False(t, findActorActivation(resp, defaultActorType, defaultActorId))
		require.True(t, findActorMethodInvokation(resp, defaultActorType, defaultActorId))
		require.False(t, findActorDeactivation(resp, defaultActorType, defaultActorId))
	})

	// Wait until the actor is deactivated
	time.Sleep(5 * time.Second)

	t.Log("Enter Test Actor PubSub GRPC")
	// PUBLISH with gRPC protocol (different topic)
	protocol = "grpc"
	t.Run(fmt.Sprintf("%s_%s", "actor-pubsub-publish", protocol), func(t *testing.T) {
		_, err := sendToPublisher(t, actorPubsubExternalURL, "myactorpubsubtopic", protocol, nil, "")
		require.NoError(t, err)

		// Wait for actor to receive the method
		time.Sleep(2 * time.Second)

		// Check actors log for activation
		logsURL := fmt.Sprintf(actorlogsURLFormat, actorPubsubExternalURL)

		fmt.Printf("getting logs, the current time is %s\n", time.Now())
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		require.False(t, findActorActivation(resp, defaultActorType, defaultActorId))
		require.True(t, findActorMethodInvokation(resp, defaultActorType, defaultActorId))
		require.True(t, findActorDeactivation(resp, defaultActorType, defaultActorId))
	})

	// Check default subscription (Without sending actor type)
	t.Run(fmt.Sprintf("%s_%s", "actor-pubsub-publish-no-actortype", protocol), func(t *testing.T) {
		_, err := sendToPublisherNoType(t, actorPubsubExternalURL, "myactorpubsubtopic", protocol, nil, "")
		require.NoError(t, err)

		// Wait for actor to receive the method
		time.Sleep(2 * time.Second)

		// Check actors logs
		logsURL := fmt.Sprintf(actorlogsURLFormat, actorPubsubExternalURL)

		fmt.Printf("getting logs, the current time is %s\n", time.Now())
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		// Check first subscriber was activated
		require.False(t, findActorActivation(resp, firstSubActorType, defaultActorId))
		require.True(t, findActorMethodInvokation(resp, firstSubActorType, defaultActorId))
		require.False(t, findActorDeactivation(resp, firstSubActorType, defaultActorId))

		// Check second subscriber was not activated
		require.False(t, findActorActivation(resp, SecondSubActorType, defaultActorId))
		require.False(t, findActorMethodInvokation(resp, SecondSubActorType, defaultActorId))
		require.False(t, findActorDeactivation(resp, SecondSubActorType, defaultActorId))
	})
}
