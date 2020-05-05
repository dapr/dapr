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

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of get calls before starting tests.

var tr *runner.TestRunner

const (
	runtimeAppName     = "runtime"
	runtimeInitAppName = "runtime-init"
	numRedisMessages   = 10
	numKafkaMessages   = 1
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

var kafkaPort int

func getAPIResponse(t *testing.T, testName, runtimeExternalURL string) (*daprAPIResponse, error) {
	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/%s", runtimeExternalURL, testName)

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

func TestMain(m *testing.M) {
	fmt.Println("Enter TestMain")
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	initApps := []kube.AppDescription{
		{
			AppName:        runtimeInitAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-runtime_init",
			Replicas:       1,
			IngressEnabled: true,
			AppPort:        -1,
		},
	}

	testApps := []kube.AppDescription{
		{
			AppName:        runtimeAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-runtime",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("runtimetest", testApps, nil, initApps)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestRuntimeInitPubsub(t *testing.T) {
	t.Log("Enter TestRuntimeInitPubsub")

	// Get subscriber app URL
	runtimeExternalURL := tr.Platform.AcquireAppExternalURL(runtimeAppName)
	require.NotEmpty(t, runtimeExternalURL, "runtimeExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(runtimeExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Get API responses from subscriber
	apiResponse, err := getAPIResponse(t, "pubsub", runtimeExternalURL)
	require.NoError(t, err)

	// Assert
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, numRedisMessages, apiResponse.DaprHTTPSuccess)
}

func TestRuntimeInitBindings(t *testing.T) {
	t.Log("Enter TestRuntimeInitBindings")

	// Get subscriber app URL
	runtimeExternalURL := tr.Platform.AcquireAppExternalURL(runtimeAppName)
	require.NotEmpty(t, runtimeExternalURL, "runtimeExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(runtimeExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Get API responses from subscriber
	apiResponse, err := getAPIResponse(t, "bindings", runtimeExternalURL)
	require.NoError(t, err)

	// Assert
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, numKafkaMessages, apiResponse.DaprHTTPSuccess)
}
