// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------
package runtime_e2e

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of get calls before starting tests.

var tr *runner.TestRunner

const (
	publisherAppName  = "runtime-publisher"
	subscriberAppName = "runtime-subscriber"
)

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type daprAPIResponse struct {
	DaprHTTPSuccess int `json:"dapr_http_success"`
	DaprHTTPError   int `json:"dapr_http_error"`
	// TODO: gRPC API
}

func getAPIResponse(t *testing.T, subscriberExternalURL string) (*daprAPIResponse, error) {
	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/response", subscriberExternalURL)

	resp, err := http.Get(url)
	defer resp.Body.Close()

	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var appResp daprAPIResponse
	err = json.Unmarshal(body, &appResp)
	require.NoError(t, err)

	return &appResp, nil
}

func publishMessages(addr, password, topic string, numMessages int) error {
	opts := &redis.Options{
		Addr:            addr,
		Password:        password,
		DB:              0,
		MaxRetries:      3,
		MaxRetryBackoff: time.Second * 2,
	}

	client := redis.NewClient(opts)

	for i := 0; i < numMessages; i++ {
		_, err := client.XAdd(&redis.XAddArgs{
			Stream: topic,
			Values: map[string]interface{}{"data": fmt.Sprintf("message%d", i)},
		}).Result()
		if err != nil {
			return fmt.Errorf("redis streams: error from publish: %s", err)
		}
	}

	return nil
}

func TestMain(m *testing.M) {
	fmt.Println("Enter TestMain")
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        subscriberAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-runtime-subscriber",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	var forwarder runner.PortForwarder
	preDeploy := func(platform runner.PlatformInterface) error {
		// Connect to redis
		forwarder = platform.GetPortForwarder()
		localPorts, err := forwarder.Connect("dapr-redis-master-0", 6379)
		if err != nil {
			log.Fatalf("Failed to establish tunnel to redis: %+v", err)
		}
		redisPort := localPorts[0]

		// Publish messages on to a topic
		topic := "runtime-http-api"
		redisAddr := fmt.Sprintf("localhost:%d", redisPort)
		redisPass := ""

		err = publishMessages(redisAddr, redisPass, topic, 10)
		if err != nil {
			return fmt.Errorf("Failed to publish messages: %+v", err)
		}
		return nil
	}
	postDeploy := func(platform runner.PlatformInterface) error {
		// Close connection to redis
		if forwarder != nil {
			return forwarder.Close()
		}
		return nil
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("runtimetest", testApps, nil, preDeploy, postDeploy)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestRuntimeInit(t *testing.T) {
	t.Log("Enter TestRuntimeInit")

	// Get subscriber app URL
	subscriberExternalURL := tr.Platform.AcquireAppExternalURL(subscriberAppName)
	require.NotEmpty(t, subscriberExternalURL, "subscriberExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(subscriberExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Get API responses from subscriber
	apiResponse, err := getAPIResponse(t, subscriberExternalURL)
	require.NoError(t, err)

	// Assert
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, 10, apiResponse.DaprHTTPSuccess)
}
