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

var (
	tr       *runner.TestRunner
	nonlocal *runner.TestRunner
)

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60

	// used as the exclusive max of a random number that is used as a suffix to the first message sent.  Each subsequent message gets this number+1.
	// This is random so the first message name is not the same every time.
	randomOffsetMax           = 99
	numberOfMessagesToPublish = 100

	publisherAppName  = "pubsub-publisher-nl"
	subscriberAppName = "pubsub-subscriber-nl"
	dummyAppName      = "nl-dummy-app"
)

// nonlocalAppRunnable implements the runnnable interface
type nonlocalAppRunnable struct{}

// Run mocks a runnable for a non local app
func (n nonlocalAppRunnable) Run() int {
	return 0
}

// sent to the publisher app, which will publish data to dapr
type publishCommand struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

// data returned from the subscriber app
type receivedMessagesResponse struct {
	ReceivedByTopicD []string `json:"pubsub-d-topic"`
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
			log.Printf("Sending first publish app at url %s and body %q, this log will not print for subsequent messages for same topic", url, jsonValue)
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
	sentTopicDMessages, err := sendToPublisher(t, publisherExternalURL, "pubsub-d-topic")
	require.NoError(t, err)

	return receivedMessagesResponse{
		ReceivedByTopicD: sentTopicDMessages,
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

func testPublishSubscribeSuccessfully(t *testing.T, publisherExternalURL, subscriberExternalURL, dummyAppExternalURL string) string {
	log.Printf("Test publish subscribe success flow\n")
	sentMessages := testPublish(t, publisherExternalURL)

	time.Sleep(5 * time.Second)
	validateMessagesReceivedBySubscriber(t, subscriberExternalURL, dummyAppExternalURL, sentMessages)
	return subscriberExternalURL
}

func validateMessagesReceivedBySubscriber(t *testing.T, subscriberExternalURL, dummyAppExternalURL string, sentMessages receivedMessagesResponse) {
	// get messages from dummy app (should be empty).
	// dummy app is just a proxy application so that daprd can be started along side a container.
	log.Printf("Checking that the app %s has not received any messages...\n", dummyAppName)
	appResp := getAppResponse(t, dummyAppExternalURL)

	require.Equal(t, 0, len(appResp.ReceivedByTopicD), "dummy app received message from dapr")

	// get messages from subscriber app (should be empty)
	appResp = getAppResponse(t, subscriberExternalURL)
	log.Printf("subscriber %s receieved %d messages on pubsub-d-topic", subscriberAppName, len(appResp.ReceivedByTopicD))

	// Sort messages first because the delivered messages cannot be ordered.
	sort.Strings(sentMessages.ReceivedByTopicD)
	sort.Strings(appResp.ReceivedByTopicD)

	if !reflect.DeepEqual(sentMessages.ReceivedByTopicD, appResp.ReceivedByTopicD) {
		for i := 0; i < len(sentMessages.ReceivedByTopicD); i++ {
			log.Printf("%s, %s", sentMessages.ReceivedByTopicD[i], appResp.ReceivedByTopicD[i])
		}
	}
	require.Equal(t, sentMessages.ReceivedByTopicD, appResp.ReceivedByTopicD)
}

func getAppResponse(t *testing.T, externalURL string) receivedMessagesResponse {
	// this is the app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/getMessages", externalURL)
	log.Printf("Getting messages received by app using url %s", url)

	resp, err := utils.HTTPPost(url, nil)
	require.NoError(t, err)

	var appResp receivedMessagesResponse
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err)
	return appResp
}

func runTests(m *testing.M) int {
	// the non-local app is run without a daprd sidecar.
	nonlocalApps := []kube.AppDescription{
		{
			AppName:        subscriberAppName,
			DaprEnabled:    false,
			ImageName:      "e2e-pubsub-subscriber",
			Replicas:       1,
			IngressEnabled: false,
		},
	}

	log.Printf("Creating non-local app runner\n")
	nonlocal = runner.NewTestRunner("nonlocalrunner", nonlocalApps, nil, nil)
	log.Printf("Starting non-local dapr disabled apps\n")
	nonlocal.StartNonLocalDaprDisabledApps()
	defer func() {
		log.Printf("Running non-local dapr disabled apps teardown...")
		nonlocal.TearDown()
	}()

	host, err := nonlocal.Platform.GetServiceDNSName(subscriberAppName)
	if err != nil {
		log.Printf("error getting host fqdn %s", err.Error())
		return 1
	}

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
			// The dummyApp is run with a sidecar which connects to the non-local app defined above using the AppHost.
			AppName:        dummyAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-pubsub-subscriber",
			Replicas:       1,
			IngressEnabled: true,
			AppHost:        host, // The dapr sidecar will connect to app reachable by host
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("nlpubsubtest", testApps, nil, nil)
	log.Printf("Starting TestRunner\n")
	return tr.Start(m)
}

func TestMain(m *testing.M) {
	log.Println("Enter TestMain")
	os.Exit(runTests(m))
}

func TestPubSubNonLocal(t *testing.T) {
	log.Println("Enter TestPubSubNonLocal")
	publisherExternalURL := tr.Platform.AcquireAppExternalURL(publisherAppName)
	require.NotEmpty(t, publisherExternalURL, "publisherExternalURL must not be empty!")

	dummyAppExternalURL := tr.Platform.AcquireAppExternalURL(dummyAppName)
	require.NotEmpty(t, dummyAppExternalURL, "dummyAppExternalURL must not be empty!")

	// actual subscriber non-local app does not have an ingress so port-forward to the application port.
	localPort, err := nonlocal.Platform.PortForwardToApp(subscriberAppName, 3000)
	require.NoError(t, err, "error port forwarding to subscriber")
	require.Equal(t, 1, len(localPort), "localPort must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(publisherExternalURL, numHealthChecks)
	require.NoError(t, err)

	_, err = utils.HTTPGetNTimes(dummyAppExternalURL, numHealthChecks)
	require.NoError(t, err)

	// port forwarded call to subscriber application.
	subscriberURL := fmt.Sprintf("127.0.0.1:%v", localPort[0])
	_, err = utils.HTTPGetNTimes("http://"+subscriberURL, numHealthChecks)
	require.NoError(t, err)

	t.Run("publish and subscribe message successfully", func(t *testing.T) {
		testPublishSubscribeSuccessfully(t, publisherExternalURL, subscriberURL, dummyAppExternalURL)
	})
}
