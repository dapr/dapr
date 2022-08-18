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

package pubsub_extra_e2e

// This test performs additional E2E tests for pubsub that are not part of the "core" capabilities, including:
// - Testing wildcard subscriptions (using in-memory pubsub)
// - Testing dynamic subscribe/unsubscribe

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	testutils "github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/utils"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60
)

func TestMain(m *testing.M) {
	testutils.SetupLogs("pubsub-extra")
	testutils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "pubsub-extra-http",
			AppPort:        4000,
			AppProtocol:    "http",
			DaprEnabled:    true,
			IngressEnabled: true,
			IngressPort:    3000,
			ImageName:      "e2e-pubsub-extra",
			Replicas:       1,
			AppEnv: map[string]string{
				"APP_PROTOCOL": "http",
				"APP_PORT":     "4000",
				"CONTROL_PORT": "3000",
			},
		},
		{
			AppName:        "pubsub-extra-grpc",
			AppPort:        4000,
			AppProtocol:    "grpc",
			DaprEnabled:    true,
			IngressEnabled: true,
			IngressPort:    3000,
			ImageName:      "e2e-pubsub-extra",
			Replicas:       1,
			AppEnv: map[string]string{
				"APP_PROTOCOL": "grpc",
				"APP_PORT":     "4000",
				"CONTROL_PORT": "3000",
			},
		},
	}

	log.Print("Creating TestRunner")
	tr = runner.NewTestRunner("pubsubextratest", testApps, nil, nil)
	log.Print("Starting TestRunner")
	os.Exit(tr.Start(m))
}

func TestPubSubExtraHTTP(t *testing.T) {
	testPubSubExtraProtocol(t, "http")
}

func TestPubSubExtraGRPC(t *testing.T) {
	testPubSubExtraProtocol(t, "grpc")
}

func testPubSubExtraProtocol(t *testing.T, protocol string) {
	appName := "pubsub-extra-" + protocol
	appExternalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, appExternalURL, "appExternalURL must not be empty!")

	if !strings.HasPrefix(appExternalURL, "http://") && !strings.HasPrefix(appExternalURL, "https://") {
		appExternalURL = "http://" + appExternalURL
	}
	appExternalURL = strings.TrimSuffix(appExternalURL, "/")

	log.Println("App external URL", appExternalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := testutils.HTTPGetNTimes(appExternalURL, numHealthChecks)
	require.NoError(t, err)

	publishUrl := fmt.Sprintf("%s/publish", appExternalURL)
	messagesUrl := fmt.Sprintf("%s/messages", appExternalURL)
	resetUrl := fmt.Sprintf("%s/reset-messages", appExternalURL)

	var currentSubs []string

	t.Run("validate initial subscription", func(t *testing.T) {
		u := fmt.Sprintf("%s/subscriptions", appExternalURL)
		log.Println("Invoking method", "GET", u)
		res, err := testutils.HTTPGet(u)
		require.NoError(t, err)

		list, listRaw, err := parseSubscriptionList(res)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "inmemorypubsub", list[0].PubsubName)
		assert.Equal(t, "mytopic", list[0].Topic)

		currentSubs = listRaw

		if protocol == "http" {
			// The routes are always converted to the new format
			assert.Len(t, list[0].Routes.Rules, 1)
			assert.Equal(t, "", list[0].Routes.Default)
			assert.Equal(t, "/message/mytopic", list[0].Routes.Rules[0].Path)
			assert.Equal(t, "", list[0].Routes.Rules[0].Match)
		} else {
			// Somehow this does include an object here, although it's essentially empty
			// Expected is: `{"subscriptions":[{"pubsubname":"inmemorypubsub","topic":"mytopic","routes":{"rules":[{}]}}]}`
			if list[0].Routes != nil {
				assert.Equal(t, "", list[0].Routes.Default)

				if len(list[0].Routes.Rules) > 0 {
					assert.Len(t, list[0].Routes.Rules, 1)
					assert.Empty(t, list[0].Routes.Rules[0].Match)
					assert.Empty(t, list[0].Routes.Rules[0].Path)
				}
			}
		}
	})

	t.Run("subscribe to topics", func(t *testing.T) {
		u := fmt.Sprintf("%s/subscriptions", appExternalURL)

		tests := []struct {
			Name        string
			ReqBody     string
			ExpectError bool
			ExpectSubs  []string
		}{
			{
				Name:    "topic myothertopic",
				ReqBody: `{"pubsubname":"inmemorypubsub","topic":"myothertopic","routes":{"default":"/message/myothertopic"}}`,
				ExpectSubs: append([]string{
					`{"pubsubname":"inmemorypubsub","topic":"myothertopic","routes":{"rules":[{"path":"/message/myothertopic"}]}}`,
				}, currentSubs...),
			},
			{
				Name:    "topic tt*",
				ReqBody: `{"pubsubname":"inmemorypubsub","topic":"tt*","routes":{"default":"/message/tt"}}`,
				ExpectSubs: append([]string{
					`{"pubsubname":"inmemorypubsub","topic":"myothertopic","routes":{"rules":[{"path":"/message/myothertopic"}]}}`,
					`{"pubsubname":"inmemorypubsub","topic":"tt*","routes":{"rules":[{"path":"/message/tt"}]}}`,
				}, currentSubs...),
			},
			{
				Name:        "missing topic field",
				ReqBody:     `{"pubsubname":"inmemorypubsub","routes":{"default":"/foo"}}`,
				ExpectError: true,
			},
			{
				Name:        "missing pubsubname field",
				ReqBody:     `{"topic":"foo"}`,
				ExpectError: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.Name, func(t *testing.T) {
				log.Println("Invoking method", "POST", u)
				res, status, err := testutils.HTTPPostWithStatus(u, []byte(tt.ReqBody))
				require.NoError(t, err)
				if tt.ExpectError {
					require.Equal(t, http.StatusServiceUnavailable, status, "Expected Dapr to return an error, but got %d", status)
				} else {
					if status == http.StatusServiceUnavailable {
						// Error from Dapr
						t.Errorf("Dapr returned an unexpected error: %v", string(res))
						t.FailNow()
					} else if status < 200 || status > 399 {
						// Other error
						t.Errorf("Unexpected error (%d): %v", status, string(res))
						t.FailNow()
					}

					_, listRaw, err := parseSubscriptionList(res)
					require.NoError(t, err)
					expectSubscriptions(t, tt.ExpectSubs, listRaw)

					currentSubs = listRaw
				}
			})
		}
	})

	t.Run("list subscriptions after adding", func(t *testing.T) {
		u := fmt.Sprintf("%s/subscriptions", appExternalURL)
		log.Println("Invoking method", "GET", u)
		res, err := testutils.HTTPGet(u)
		require.NoError(t, err)

		_, listRaw, err := parseSubscriptionList(res)
		require.NoError(t, err)
		expectSubscriptions(t, currentSubs, listRaw)
	})

	t.Run("publish and receive", func(t *testing.T) {
		tests := []publishReceiveTest{
			{
				Name:   "publish on mytopic",
				Topics: []string{"mytopic"},
				Count:  5,
			},
			{
				Name:   "publish on mytopic and myothertopic",
				Topics: []string{"mytopic", "myothertopic"},
				Count:  5,
			},
			{
				Name:   "publish on wildcard topics",
				Topics: []string{"tt1", "tt2", "ttfoo"},
				Count:  5,
				Wildcards: map[string]string{
					"tt1":   "tt",
					"tt2":   "tt",
					"ttfoo": "tt",
				},
			},
			{
				Name:        "publish on non-existing topic",
				Topics:      []string{"ttx", "foott"},
				Count:       5,
				NonExistent: []string{"foott"},
				Wildcards: map[string]string{
					"ttx": "tt",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.Name, testPublishReceiveFunc(tt, publishUrl, messagesUrl, resetUrl))
		}
	})

	t.Run("unsubscribe from topic", func(t *testing.T) {
		u := fmt.Sprintf("%s/subscriptions", appExternalURL)

		t.Run("unsubscribe successfully", func(t *testing.T) {
			log.Println("Invoking method", "DELETE", u)
			res, status, err := testutils.HTTPDeleteWithBodyAndStatus(u, []byte(`{"pubsubname":"inmemorypubsub","topic":"myothertopic"}`))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)

			// Remove from currentSubs
			remove := utils.CompactJSON(`{"pubsubname":"inmemorypubsub","topic":"myothertopic","routes":{"rules":[{"path":"/message/myothertopic"}]}}`)
			n := 0
			for _, v := range currentSubs {
				if utils.CompactJSON(v) == remove {
					continue
				}
				currentSubs[n] = v
				n++
			}
			currentSubs = currentSubs[:n]

			_, listRaw, err := parseSubscriptionList(res)
			require.NoError(t, err)
			expectSubscriptions(t, currentSubs, listRaw)
		})

		t.Run("subscription does not exist", func(t *testing.T) {
			log.Println("Invoking method", "DELETE", u)
			res, status, err := testutils.HTTPDeleteWithBodyAndStatus(u, []byte(`{"pubsubname":"noexist","topic":"noexist"}`))
			require.NoError(t, err)
			require.Equal(t, http.StatusServiceUnavailable, status)
			require.Contains(t, string(res), "the subscription does not exist")
		})
	})

	t.Run("list subscriptions after removing", func(t *testing.T) {
		u := fmt.Sprintf("%s/subscriptions", appExternalURL)
		log.Println("Invoking method", "GET", u)
		res, err := testutils.HTTPGet(u)
		require.NoError(t, err)

		_, listRaw, err := parseSubscriptionList(res)
		require.NoError(t, err)
		expectSubscriptions(t, currentSubs, listRaw)
	})

	t.Run("publish and receive", func(t *testing.T) {
		tests := []publishReceiveTest{
			{
				Name:        "publish on mytopic and non-existent myothertopic",
				Topics:      []string{"mytopic", "myothertopic"},
				Count:       5,
				NonExistent: []string{"myothertopic"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.Name, testPublishReceiveFunc(tt, publishUrl, messagesUrl, resetUrl))
		}
	})
}

type publishReceiveTest struct {
	Name        string
	Topics      []string
	Count       int
	NonExistent []string
	Wildcards   map[string]string
}

func testPublishReceiveFunc(tt publishReceiveTest, publishUrl, messagesUrl, resetUrl string) func(t *testing.T) {
	return func(t *testing.T) {
		// Publish
		expect := map[string][]string{}
		messages := []json.RawMessage{}
		for _, topic := range tt.Topics {
			exists := true
			for _, v := range tt.NonExistent {
				if v == topic {
					exists = false
					break
				}
			}

			expectKey := topic
			if exists {
				wc := tt.Wildcards[topic]
				if wc != "" {
					expectKey = wc
				}
				if _, ok := expect[expectKey]; !ok {
					expect[expectKey] = make([]string, 0)
				}
			}
			for i := 0; i < tt.Count; i++ {
				data := fmt.Sprintf(`{"orderId":"%s-%d"}`, topic, i+1)
				msg := &runtimev1pb.PublishEventRequest{
					PubsubName:      "inmemorypubsub",
					Topic:           topic,
					Data:            []byte(data),
					DataContentType: "application/json",
				}
				enc, _ := protojson.Marshal(msg)
				messages = append(messages, enc)
				if exists {
					expect[expectKey] = append(expect[expectKey], data)
				}
			}
		}

		log.Println("Invoking method", "POST", publishUrl)
		body, _ := json.Marshal(messages)
		res, err := testutils.HTTPPost(publishUrl, body)
		require.NoError(t, err)
		assert.Equal(t, []byte("👍"), res)

		// Receive
		attempts := 0
		var received map[string][]string
		for attempts < 3 {
			// The pubsub is in-memory so 2 seconds should be plenty for messages to be delivered
			time.Sleep(2 * time.Second)

			received, err = getDeliveredMessages(messagesUrl)
			if err != nil {
				log.Println("Error returned by getDeliveredMessages", err)
				attempts++
				continue
			}

			if !reflect.DeepEqual(received, expect) {
				attempts++
				continue
			}

			break
		}

		require.Less(t, attempts, 3, "too many attempts; expected='%v'; last received list='%v'", expect, received)

		// Reset
		log.Println("Invoking method", "POST", resetUrl)
		_, err = testutils.HTTPPost(resetUrl, []byte{})
		require.NoError(t, err)
	}
}

func getDeliveredMessages(u string) (map[string][]string, error) {
	log.Println("Invoking method", "GET", u)
	res, err := testutils.HTTPGet(u)
	if err != nil {
		return nil, err
	}

	messages := map[string][]string{}
	err = json.Unmarshal(res, &messages)
	if err != nil {
		return nil, err
	}

	for k := range messages {
		sort.Strings(messages[k])
	}

	return messages, nil
}

func parseSubscriptionList(data []byte) ([]*commonv1pb.TopicSubscription, []string, error) {
	root := struct {
		Subscriptions []json.RawMessage
	}{}
	err := json.Unmarshal(data, &root)
	if err != nil {
		return nil, nil, err
	}

	list := make([]*commonv1pb.TopicSubscription, len(root.Subscriptions))
	listRaw := make([]string, len(root.Subscriptions))
	for i, raw := range root.Subscriptions {
		el := &commonv1pb.TopicSubscription{}
		err = protojson.Unmarshal(raw, el)
		if err != nil {
			return nil, nil, err
		}

		list[i] = el
		listRaw[i] = string(raw)
	}

	return list, listRaw, nil
}

func expectSubscriptions(t *testing.T, expect []string, actual []string) {
	a := make([]string, len(actual))
	for i, v := range actual {
		a[i] = utils.CompactJSON(v)
	}
	e := make([]string, len(expect))
	for i, v := range expect {
		e[i] = utils.CompactJSON(v)
	}
	sort.Strings(e)
	sort.Strings(a)
	assert.Equal(t, e, a)
}
