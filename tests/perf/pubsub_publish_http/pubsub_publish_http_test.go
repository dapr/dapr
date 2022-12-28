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

package pubsub_publish_http

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
	"github.com/stretchr/testify/require"
)

var (
	tr *runner.TestRunner
)

const (
	k6AppName     = "k6-test-app"
	brokersEnvVar = "BROKERS"
)

var brokers = []kube.ComponentDescription{
	{
		Name:      "redis-broker",
		Namespace: &kube.DaprTestNamespace,
		TypeName:  "pubsub.redis",
		MetaData: map[string]kube.MetadataValue{
			"redisHost": {
				FromSecretRef: &kube.SecretRef{
					Name: "redissecret",
					Key:  "host",
				},
			},
			"redisPassword": {Raw: `""`},
			"consumerID":    {Raw: `"myGroup"`},
			"enableTLS":     {Raw: `"false"`},
		},
		Scopes: []string{k6AppName},
	},
	{
		Name:      "kafka-broker",
		Namespace: &kube.DaprTestNamespace,
		TypeName:  "pubsub.kafka",
		MetaData: map[string]kube.MetadataValue{
			"brokers":              {Raw: `"dapr-kafka:9092"`},
			"authType":             {Raw: `"none"`},
			"maxMessageBytes":      {Raw: `"1000000"`},
			"consumeRetryInterval": {Raw: `"200ms"`},
			"disableTls":           {Raw: `"true"`},
			"skipVerify":           {Raw: `"true"`},
			"initialOffset":        {Raw: `"oldest"`},
		},
		Scopes: []string{k6AppName},
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
	utils.SetupLogs("pubsub_publish_http_test")

	tr = runner.NewTestRunner("pubsub_publish_http", []kube.AppDescription{}, brokers, nil)
	os.Exit(tr.Start(m))
}

func TestPubsubPublishHttpPerformance(t *testing.T) {
	k6Test := loadtest.NewK6("./test.js", loadtest.WithName(k6AppName), loadtest.WithParallelism(1), loadtest.WithRunnerEnvVar(brokersEnvVar, brokersNames))
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
}
