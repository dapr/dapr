//go:build perf
// +build perf

/*
Copyright 2023 The Dapr Authors
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

package pubsub_bulk_publish_http

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/dapr/tests/runner/summary"
	"github.com/stretchr/testify/require"
)

var (
	tr *runner.TestRunner
)

const (
	k6AppName = "k6-test-app"
	topicName = "bulkpublishperftopic"
)

type testCase struct {
	broker        string
	publishType   string
	topic         string
	bulkSize      int
	messageSizeKb int
	durationMs    int
	numVus        int
}

var brokers = []kube.ComponentDescription{
	{
		Name:      "memory-broker",
		Namespace: &kube.DaprTestNamespace,
		TypeName:  "pubsub.in-memory",
		MetaData:  map[string]kube.MetadataValue{},
		Scopes:    []string{k6AppName},
	},
}

var brokersNames string

func init() {
	brokersList := []string{}
	for _, broker := range brokers {
		brokersList = append(brokersList, broker.Name)
	}
	brokersNames = strings.Join(brokersList, ",")
}

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_bulk_publish_http_test")

	tr = runner.NewTestRunner("pubsub_bulk_publish_http", []kube.AppDescription{}, brokers, nil)
	os.Exit(tr.Start(m))
}

// TestPubsubBulkPublishHttpPerformance compares the performance of bulk publish vs normal publish
// for different brokers, bulk sizes and message sizes.
func TestPubsubBulkPublishHttpPerformance(t *testing.T) {
	publishTypes := []string{"normal", "bulk"}
	bulkSizes := []int{10, 100}
	messageSizesKb := []int{1}

	testcases := []testCase{}
	for _, bulkSize := range bulkSizes {
		for _, messageSizeKb := range messageSizesKb {
			for _, broker := range brokers {
				for _, publishType := range publishTypes {
					testcases = append(testcases, testCase{
						broker:        broker.Name,
						publishType:   publishType,
						topic:         topicName,
						bulkSize:      bulkSize,
						messageSizeKb: messageSizeKb,
						durationMs:    30 * 1000,
						numVus:        50,
					})
				}
			}
		}
	}

	for _, tc := range testcases {
		testName := fmt.Sprintf("%s_b%d_s%dKB_%s", tc.broker, tc.bulkSize, tc.messageSizeKb, tc.publishType)
		t.Run(testName, func(t *testing.T) {
			runTest(t, tc)
		})
	}
}

func runTest(t *testing.T, tc testCase) {
	t.Logf("Starting test: %s", t.Name())

	k6Test := loadtest.NewK6(
		"./test.js",
		// loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
		loadtest.WithAppID(k6AppName),
		loadtest.WithName(k6AppName),
		loadtest.WithRunnerEnvVar("PUBLISH_TYPE", tc.publishType),
		loadtest.WithRunnerEnvVar("BROKER_NAME", tc.broker),
		loadtest.WithRunnerEnvVar("TOPIC_NAME", tc.topic),
		loadtest.WithRunnerEnvVar("BULK_SIZE", fmt.Sprintf("%d", tc.bulkSize)),
		loadtest.WithRunnerEnvVar("MESSAGE_SIZE_KB", fmt.Sprintf("%d", tc.messageSizeKb)),
		loadtest.WithRunnerEnvVar("DURATION_MS", fmt.Sprintf("%d", tc.durationMs)),
		loadtest.WithRunnerEnvVar("NUM_VUS", fmt.Sprintf("%d", tc.numVus)),
	)
	defer k6Test.Dispose()

	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	sm, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, sm)

	summary.ForTest(t).
		OutputK6(sm.RunnersResults).
		Output("Broker", tc.broker).
		Output("PublishType", tc.publishType).
		Output("BulkSize", fmt.Sprintf("%d", tc.bulkSize)).
		Output("MessageSizeKb", fmt.Sprintf("%d", tc.messageSizeKb)).
		Flush()

	bts, err := json.MarshalIndent(sm, "", " ")
	require.NoError(t, err)
	require.True(t, sm.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	t.Logf("test summary `%s`", string(bts))
}
