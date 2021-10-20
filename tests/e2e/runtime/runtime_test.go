// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------
package runtime_e2e

import (
	"encoding/json"
	"fmt"
	"io"
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
	numPubsubMessages  = 10
)

type daprAPIResponse struct {
	DaprHTTPSuccess int `json:"dapr_http_success"`
	DaprHTTPError   int `json:"dapr_http_error"`
	DaprGRPCSuccess int `json:"dapr_grpc_success"`
	DaprGRPCError   int `json:"dapr_grpc_error"`
}

func getAPIResponse(t *testing.T, testName, runtimeExternalURL string) (*daprAPIResponse, error) {
	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/%s", runtimeExternalURL, testName)

	resp, err := utils.HTTPGetRaw(url)
	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
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
	// These apps and components will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	comps := []kube.ComponentDescription{
		{
			Name:     "runtime-bindings-http",
			TypeName: "bindings.kafka",
			MetaData: map[string]string{
				"brokers":       `"dapr-kafka:9092"`,
				"topics":        `"runtime-bindings-http"`,
				"consumerGroup": `"group1"`,
				"authRequired":  `"false"`,
			},
		},
	}

	initApps := []kube.AppDescription{
		{
			AppName:        runtimeInitAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-runtime_init",
			Replicas:       1,
			IngressEnabled: false,
			MetricsEnabled: true,
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
			MetricsEnabled: true,
		},
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("runtimetest", testApps, comps, initApps)
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

	// Assert that all message handler invocations had access to the API
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.GreaterOrEqual(t, numPubsubMessages, apiResponse.DaprHTTPSuccess)
	require.Equal(t, 0, apiResponse.DaprGRPCError)
	require.GreaterOrEqual(t, numPubsubMessages, apiResponse.DaprGRPCSuccess)
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

	// Assert that the binding was not invoked by prior messages
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, 0, apiResponse.DaprHTTPSuccess)
	require.Equal(t, 0, apiResponse.DaprGRPCError)
	require.Equal(t, 0, apiResponse.DaprGRPCSuccess)
}
