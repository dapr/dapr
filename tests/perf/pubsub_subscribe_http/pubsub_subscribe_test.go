//go:build perf
// +build perf

/*
Copyright 2022 The Dapr Authors
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

package pubsub_subscribe_http

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"testing"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

var (
	tr          *runner.TestRunner
	actorsTypes string
	configs     PubsubComponentConfig
)

const (
	testLabel        = "pubsub_subscribe_test_label"
	normalPubsubType = "normal"
	bulkPubsubType   = "bulk"
)

func getAppDescription(pubsubComponent Component, pubsubType string) kube.AppDescription {
	appDescription := kube.AppDescription{
		AppName:           pubsubComponent.TestAppName + "-" + pubsubType,
		DaprEnabled:       true,
		ImageName:         pubsubComponent.ImageName,
		Replicas:          1,
		IngressEnabled:    true,
		MetricsEnabled:    true,
		AppPort:           3000,
		AppProtocol:       "http",
		DaprCPULimit:      "4.0",
		DaprCPURequest:    "0.1",
		DaprMemoryLimit:   "512Mi",
		DaprMemoryRequest: "250Mi",
		AppCPULimit:       "4.0",
		AppCPURequest:     "0.1",
		AppMemoryLimit:    "800Mi",
		AppMemoryRequest:  "2500Mi",
		Labels: map[string]string{
			"daprtest": pubsubComponent.TestLabel + "-" + pubsubType,
		},
		AppEnv: map[string]string{
			"PUB_SUB_COMPONENT_NAME": pubsubComponent.Name,
			"PUB_SUB_TOPIC_NAME":     pubsubComponent.Topic,
			"PUB_SUB_ROUTE_NAME":     pubsubComponent.Route,
			"SUBSCRIBE_TYPE":         pubsubType,
		},
	}

	return appDescription
}

func contains(items []string, item string) bool {
	for _, value := range items {
		if value == item {
			return true
		}
	}

	return false
}

func TestMain(m *testing.M) {
	utils.SetupLogs(testLabel)

	//Read the config file for individual components
	data, err := ioutil.ReadFile("pubsub_component_config.yaml")
	if err != nil {
		fmt.Println("error reading pubsub_component_config.yaml: ", err)
		return
	}

	err = yaml.Unmarshal(data, &configs)

	//set the configuration as environment variables for the test app.
	var testApps []kube.AppDescription

	for _, pubsubComponent := range configs.Components {
		//normal pubsub app
		if contains(pubsubComponent.Operations, normalPubsubType) {
			fmt.Println("image used: ", pubsubComponent.ImageName, pubsubComponent.TestAppName, pubsubComponent.Name, normalPubsubType)
			testApps = append(testApps, getAppDescription(pubsubComponent, normalPubsubType))
		}

		//bulk pubsub app
		if contains(pubsubComponent.Operations, bulkPubsubType) {
			fmt.Println("image used: ", pubsubComponent.ImageName, pubsubComponent.TestAppName, pubsubComponent.Name, bulkPubsubType)
			testApps = append(testApps, getAppDescription(pubsubComponent, bulkPubsubType))
		}
	}

	tr = runner.NewTestRunner(testLabel, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func runTest(t *testing.T, testAppURL, publishType, subscribeType, httpReqDurationThresholdMs string, component Component) {
	t.Logf("Starting test with subscribe type %s for component %s", subscribeType, component.Name)

	k6Test := loadtest.NewK6("./test.js",
		loadtest.WithParallelism(1),
		loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
		loadtest.WithRunnerEnvVar("TARGET_URL", testAppURL),
		loadtest.WithRunnerEnvVar("PUBSUB_NAME", component.Name),
		loadtest.WithRunnerEnvVar("PUBLISH_TYPE", publishType),
		loadtest.WithRunnerEnvVar("SUBSCRIBE_TYPE", subscribeType),
		loadtest.WithRunnerEnvVar("HTTP_REQ_DURATION_THRESHOLD", httpReqDurationThresholdMs),
		loadtest.WithRunnerEnvVar("PUB_SUB_TOPIC_NAME", component.Topic),
	)
	defer k6Test.Dispose()

	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	summary, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, summary)
	bts, err := json.MarshalIndent(summary, "", " ")
	require.NoError(t, err)
	require.True(t, summary.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	t.Logf("test summary `%s`", string(bts))

	var testAppName = component.TestAppName + "-" + subscribeType

	appUsage, err := tr.Platform.GetAppUsage(testAppName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(testAppName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(testAppName)
	require.NoError(t, err)

	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)
	require.Equal(t, 0, restarts)
}

func TestPubsubBulkPublishSubscribeHttpPerformance(t *testing.T) {
	for _, component := range configs.Components {
		if !contains(component.Operations, normalPubsubType) {
			t.Logf("Normal pubsub test is not added in operations, skipping %s test for normal pubsub", component.Name)
			continue
		}

		t.Run(component.Name, func(t *testing.T) {
			t.Logf("Starting test with %s subscriber", component.Name)
			// Get the ingress external url of test app
			testAppURL := tr.Platform.AcquireAppExternalURL(component.TestAppName + "-" + normalPubsubType)
			require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

			// Check if test app endpoint is available
			t.Logf("test app url: %s", testAppURL+"/health")
			_, err := utils.HTTPGetNTimes(testAppURL+"/health", component.NumHealthChecks)
			require.NoError(t, err)

			threshold := os.Getenv("DAPR_PERF_PUBSUB_SUBSCRIBE_HTTP_THRESHOLD")
			if threshold == "" {
				threshold = strconv.Itoa(component.SubscribeHttpThresholdMs)
			}

			runTest(t, testAppURL, bulkPubsubType, normalPubsubType, threshold, component)
		})
	}
}

func TestPubsubBulkPublishBulkSubscribeHttpPerformance(t *testing.T) {

	for _, component := range configs.Components {
		if !contains(component.Operations, bulkPubsubType) {
			t.Logf("Bulk pubsub test is not added in operations, skipping %s test for bulk pubsub", component.Name)
			continue
		}

		t.Run(component.Name, func(t *testing.T) {
			// Get the ingress external url of test app
			bulkTestAppURL := tr.Platform.AcquireAppExternalURL(component.TestAppName + "-" + bulkPubsubType)
			require.NotEmpty(t, bulkTestAppURL, "test app external URL must not be empty")

			// Check if test app endpoint is available
			t.Logf("bulk test app url: %s", bulkTestAppURL+"/health")
			_, err := utils.HTTPGetNTimes(bulkTestAppURL+"/health", component.NumHealthChecks)
			require.NoError(t, err)

			threshold := os.Getenv("DAPR_PERF_PUBSUB_BULK_SUBSCRIBE_HTTP_THRESHOLD")
			if threshold == "" {
				threshold = strconv.Itoa(component.BulkSubscribeHttpThresholdMs)
			}

			runTest(t, bulkTestAppURL, bulkPubsubType, bulkPubsubType, threshold, component)
		})
	}
}
