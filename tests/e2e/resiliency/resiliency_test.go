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

package resiliencyapp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/kit/ptr"
)

type FailureMessage struct {
	ID              string         `json:"id"`
	MaxFailureCount *int           `json:"maxFailureCount,omitempty"`
	Timeout         *time.Duration `json:"timeout,omitempty"`
	ResponseCode    *int           `json:"responseCode,omitempty"`
}

type CallRecord struct {
	Count    int
	TimeSeen time.Time
}

const (
	// Number of times to call the endpoint to check for health.
	numHealthChecks = 60
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("resiliency")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:        "resiliencyapp",
			DaprEnabled:    true,
			ImageName:      "e2e-resiliencyapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "resiliencyappgrpc",
			DaprEnabled:    true,
			ImageName:      "e2e-resiliencyapp_grpc",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			AppProtocol:    "grpc",
		},
	}

	tr = runner.NewTestRunner("resiliencytest", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestInputBindingResiliency(t *testing.T) {
	testCases := []struct {
		Name               string
		FailureCount       *int
		Timeout            *time.Duration
		shouldFail         bool
		binding            string
		responseStatusCode *int
	}{
		{
			Name:         "Test sending input binding to app recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			binding:      "dapr-resiliency-binding",
		},
		{
			Name:         "Test sending input binding to app recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			binding:      "dapr-resiliency-binding",
		},
		{
			Name:         "Test exhausting retries leads to failure",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			binding:      "dapr-resiliency-binding",
		},
		{
			Name:               "Test resiliency with matching consider non-500 as success",
			FailureCount:       ptr.Of(0), // since responseStatusCode is set, constantly fails with 404, this is the expected failure count
			shouldFail:         false,
			responseStatusCode: ptr.Of(404),
			binding:            "dapr-resiliency-binding",
		},
		{
			Name:               "Test resiliency with matching retry on 500",
			shouldFail:         true,
			responseStatusCode: ptr.Of(500),
			binding:            "dapr-resiliency-binding",
		},
		{
			Name:         "Test sending input binding to grpc app recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			binding:      "dapr-resiliency-binding-grpc",
		},
		{
			Name:         "Test sending input binding to grpc app recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			binding:      "dapr-resiliency-binding-grpc",
		},
		{
			Name:         "Test exhausting retries leads to failure in grpc app",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			binding:      "dapr-resiliency-binding-grpc",
		},
	}

	// Get application URLs/wait for healthy.
	externalURL := tr.Platform.AcquireAppExternalURL("resiliencyapp")
	require.NotEmpty(t, externalURL, "resiliency external URL must not be empty!")
	externalURLGRPC := tr.Platform.AcquireAppExternalURL("resiliencyappgrpc")
	require.NotEmpty(t, externalURLGRPC, "resiliencygrpc external URL must not be empty!")
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			message := createFailureMessage(tc.FailureCount, tc.Timeout, tc.responseStatusCode)
			b, _ := json.Marshal(message)
			_, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/invokeBinding/%s", externalURL, tc.binding), b)
			require.NoError(t, err)
			require.Equal(t, 200, code)

			// Let the binding propagate and give time for retries/timeout.
			time.Sleep(time.Second * 5)

			var callCount map[string][]CallRecord
			var getCallsURL string
			if strings.Contains(tc.binding, "grpc") {
				getCallsURL = "tests/getCallCountGRPC"
			} else {
				getCallsURL = "tests/getCallCount"
			}
			resp, err := utils.HTTPGet(fmt.Sprintf("%s/%s", externalURL, getCallsURL))
			require.NoError(t, err)

			err = json.Unmarshal(resp, &callCount)
			require.NoError(t, err)
			if tc.shouldFail {
				// First call + 5 retries and no more.
				require.GreaterOrEqual(t, len(callCount[message.ID]), 6, fmt.Sprintf("Call count mismatch for message %s", message.ID))

				// TODO: Remove this once we can control Kafka's retry count.
				// We have to do this because we can't currently control Kafka's retries. So, we make sure that anything past the resiliency
				// retries have a wide enough gap in them to be considered OK.
				if len(callCount[message.ID]) > 6 {
					// This is the default Kafka retry time. Our policy time is 10ms so it should be much faster.
					require.Greater(t, callCount[message.ID][6].TimeSeen.Sub(callCount[message.ID][5].TimeSeen), time.Millisecond*100)
				}
			} else {
				expected := 1 + *tc.FailureCount
				// When the binding returns a non-500 status code that is treated as
				// success (e.g. 404 with FailureCount=0) the at-least-once delivery
				// guarantee of the binding may cause an extra invocation before the
				// runtime records success, so we allow expected or expected+1 calls.
				if tc.responseStatusCode != nil && *tc.responseStatusCode != 500 && *tc.FailureCount == 0 {
					require.LessOrEqual(t, expected, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
					require.LessOrEqual(t, len(callCount[message.ID]), expected+1, fmt.Sprintf("Call count mismatch for message %s: expected %d..%d got %d", message.ID, expected, expected+1, len(callCount[message.ID])))
				} else {
					// First call + FailureCount retries including recovery.
					require.Equal(t, expected, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
				}
			}
		})
	}
}

func TestPubsubSubscriptionResiliency(t *testing.T) {
	testCases := []struct {
		Name               string
		FailureCount       *int
		Timeout            *time.Duration
		shouldFail         bool
		pubsub             string
		topic              string
		responseStatusCode *int
	}{
		{
			Name:         "Test sending event to app recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-http",
		},
		{
			Name:         "Test sending event to app recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-http",
		},
		{
			Name:         "Test exhausting retries leads to failure",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-http",
		},
		{
			Name:               "Test resiliency with matching consider non-500 as success",
			FailureCount:       ptr.Of(0), // since responseStatusCode is set, constantly fails with 400, this is the expected failure count
			shouldFail:         false,
			responseStatusCode: ptr.Of(400),
			pubsub:             "dapr-resiliency-pubsub",
			topic:              "resiliency-topic-http",
		},
		{
			Name:               "Test resiliency with matching retry on 500",
			shouldFail:         true,
			responseStatusCode: ptr.Of(500),
			pubsub:             "dapr-resiliency-pubsub",
			topic:              "resiliency-topic-http",
		},
		{
			Name:         "Test sending event to grpc app recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-grpc",
		},
		{
			Name:         "Test sending event to grpc app recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-grpc",
		},
		{
			Name:         "Test exhausting retries leads to failure in grpc app",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			pubsub:       "dapr-resiliency-pubsub",
			topic:        "resiliency-topic-grpc",
		},
	}

	// Get application URLs/wait for healthy.
	externalURL := tr.Platform.AcquireAppExternalURL("resiliencyapp")
	require.NotEmpty(t, externalURL, "resiliency external URL must not be empty!")
	externalURLGRPC := tr.Platform.AcquireAppExternalURL("resiliencyappgrpc")
	require.NotEmpty(t, externalURLGRPC, "resiliencygrpc external URL must not be empty!")
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			message := createFailureMessage(tc.FailureCount, tc.Timeout, tc.responseStatusCode)
			b, _ := json.Marshal(message)
			_, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/publishMessage/%s/%s", externalURL, tc.pubsub, tc.topic), b)
			require.NoError(t, err)
			require.Equal(t, 200, code)

			// Let the binding propagate and give time for retries/timeout.
			time.Sleep(time.Second * 10)

			var callCount map[string][]CallRecord
			var getCallsURL string
			if strings.Contains(tc.topic, "grpc") {
				getCallsURL = "tests/getCallCountGRPC"
			} else {
				getCallsURL = "tests/getCallCount"
			}
			resp, err := utils.HTTPGet(fmt.Sprintf("%s/%s", externalURL, getCallsURL))
			require.NoError(t, err)

			err = json.Unmarshal(resp, &callCount)
			require.NoError(t, err)
			if tc.shouldFail {
				// First call + 5 retries and no more.
				require.Equal(t, 6, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
			} else {
				// First call + FailureCount retries including recovery.
				require.Equal(t, 1+*tc.FailureCount, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
			}
		})
	}
}

func TestServiceInvocationResiliency(t *testing.T) {
	testCases := []struct {
		Name         string
		FailureCount *int
		Timeout      *time.Duration
		shouldFail   bool
		callType     string
		targetApp    string
		expectCount  *int
		expectStatus *int
	}{
		{
			Name:         "Test invoking app method recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			callType:     "http",
		},
		{
			Name:         "Test invoking app method recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			callType:     "http",
		},
		{
			Name:         "Test exhausting retries leads to failure",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			callType:     "http",
		},
		{
			Name:         "Test resiliency with matching consider non-500 as success",
			shouldFail:   false,
			expectStatus: ptr.Of(404),
			expectCount:  ptr.Of(1),
			callType:     "http",
		},
		{
			Name:         "Test resiliency with matching retry on 500",
			shouldFail:   true,
			expectStatus: ptr.Of(500),
			callType:     "http",
		},
		{
			Name:         "Test invoking grpc app method recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			callType:     "grpc",
		},
		{
			Name:         "Test invoking grpc app method recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			callType:     "grpc",
		},
		{
			Name:         "Test exhausting retries leads to failure in grpc app",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			callType:     "grpc",
		},
		{
			Name:         "Test invoking grpc proxy method recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			callType:     "grpc_proxy",
		},
		{
			Name:         "Test invoking grpc proxy method recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			callType:     "grpc_proxy",
		},
		{
			Name:         "Test exhausting retries leads to failure in grpc proxy",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			callType:     "grpc_proxy",
		},
		{
			Name:         "Test resiliency with matching consider code non-2 as success in grpc proxy",
			expectStatus: ptr.Of(5),
			expectCount:  ptr.Of(1),
			shouldFail:   false,
			callType:     "grpc_proxy",
		},
		{
			Name:         "Test resiliency with mtching retry on 2 in grpc proxy",
			FailureCount: ptr.Of(10),
			expectStatus: ptr.Of(2),
			shouldFail:   true,
			callType:     "grpc_proxy",
		},
		{
			Name:         "Test invoking non-existent app http",
			FailureCount: ptr.Of(3),
			expectStatus: ptr.Of(500),
			callType:     "http",
			targetApp:    "badapp",
			expectCount:  ptr.Of(0),
		},
		{
			Name:         "Test invoking non-existent app grpc",
			FailureCount: ptr.Of(3),
			expectStatus: ptr.Of(500),
			callType:     "grpc",
			targetApp:    "badapp",
			expectCount:  ptr.Of(0),
		},
	}

	// Get application URLs/wait for healthy.
	externalURL := tr.Platform.AcquireAppExternalURL("resiliencyapp")
	require.NotEmpty(t, externalURL, "resiliency external URL must not be empty!")
	externalURLGRPC := tr.Platform.AcquireAppExternalURL("resiliencyappgrpc")
	require.NotEmpty(t, externalURLGRPC, "resiliencygrpc external URL must not be empty!")
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			message := createFailureMessage(tc.FailureCount, tc.Timeout, tc.expectStatus)
			b, _ := json.Marshal(message)
			u := fmt.Sprintf("%s/tests/invokeService/%s", externalURL, tc.callType)
			if tc.targetApp != "" {
				qs := url.Values{
					"target_app": []string{tc.targetApp},
				}
				u += "?" + qs.Encode()
			}
			_, code, err := utils.HTTPPostWithStatus(u, b)
			require.NoError(t, err)
			if tc.callType == "http" {
				switch {
				case tc.expectStatus != nil:
					require.Equal(t, *tc.expectStatus, code)
				case tc.shouldFail:
					require.Equal(t, 500, code)
				default:
					require.Equal(t, 200, code)
				}
			}

			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				var callCount map[string][]CallRecord
				getCallsURL := "tests/getCallCount"
				if strings.Contains(tc.callType, "grpc") {
					getCallsURL = "tests/getCallCountGRPC"
				}
				resp, err := utils.HTTPGet(fmt.Sprintf("%s/%s", externalURL, getCallsURL))
				require.NoError(t, err)

				err = json.Unmarshal(resp, &callCount)
				require.NoError(t, err)
				switch {
				case tc.expectCount != nil:
					assert.Equal(c, *tc.expectCount, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
				case tc.shouldFail:
					// First call + 5 retries and no more.
					assert.Equal(c, 6, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
				default:
					// First call + 3 retries and recovery.
					assert.Equal(c, 4, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
				}
			}, time.Second*30, time.Second)
		})
	}
}

func TestActorResiliency(t *testing.T) {
	testCases := []struct {
		Name         string
		FailureCount *int
		Timeout      *time.Duration
		shouldFail   bool
		protocol     string
	}{
		{
			Name:         "Test invoking actor recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			protocol:     "http",
		},
		{
			Name:         "Test invoking actor recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			protocol:     "http",
		},
		{
			Name:         "Test exhausting retries leads to failure",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			protocol:     "http",
		},
		{
			Name:         "Test invoking actor with grpc recovers from failure",
			FailureCount: ptr.Of(3),
			shouldFail:   false,
			protocol:     "grpc",
		},
		{
			Name:         "Test invoking actor with grpc recovers from timeout",
			FailureCount: ptr.Of(3),
			Timeout:      ptr.Of(time.Second * 2),
			shouldFail:   false,
			protocol:     "grpc",
		},
		{
			Name:         "Test exhausting retries leads to failure in grpc actor call",
			FailureCount: ptr.Of(10),
			shouldFail:   true,
			protocol:     "grpc",
		},
	}

	// Get application URLs/wait for healthy.
	externalURL := tr.Platform.AcquireAppExternalURL("resiliencyapp")
	require.NotEmpty(t, externalURL, "resiliency external URL must not be empty!")
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			message := createFailureMessage(tc.FailureCount, tc.Timeout, nil)
			b, _ := json.Marshal(message)
			_, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/invokeActor/%s", externalURL, tc.protocol), b)
			require.NoError(t, err)
			if !tc.shouldFail {
				require.Equal(t, 200, code)
			} else {
				require.Equal(t, 500, code)
			}

			// Let the binding propagate and give time for retries/timeout.
			time.Sleep(time.Second * 5)

			var callCount map[string][]CallRecord
			resp, err := utils.HTTPGet(fmt.Sprintf("%s/tests/getCallCount", externalURL))
			require.NoError(t, err)

			err = json.Unmarshal(resp, &callCount)
			require.NoError(t, err)
			if tc.shouldFail {
				// First call + 5 retries and no more.
				require.GreaterOrEqual(t, len(callCount[message.ID]), 6, fmt.Sprintf("Call count mismatch for message %s", message.ID))

				// TODO: Remove this once we can control Kafka's retry count.
				// We have to do this because we can't currently control Kafka's retries. So, we make sure that anything past the resiliency
				// retries have a wide enough gap in them to be considered OK.
				if len(callCount[message.ID]) > 6 {
					// This is the default Kafka retry time. Our policy time is 10ms so it should be much faster.
					require.Greater(t, callCount[message.ID][6].TimeSeen.Sub(callCount[message.ID][5].TimeSeen), time.Millisecond*100)
				}
			} else {
				// First call + 3 retries and recovery.
				require.Equal(t, 4, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
			}
		})
	}
}

func TestResiliencyCircuitBreakers(t *testing.T) {
	testCases := []struct {
		Name     string
		CallType string
	}{
		{
			Name:     "Test http service invocation circuit breaker trips",
			CallType: "http",
		},
		{
			Name:     "Test grpc service invocation circuit breaker trips",
			CallType: "grpc",
		},
		{
			Name:     "Test grpc proxy invocation circuit breaker trips",
			CallType: "grpc_proxy",
		},
	}

	// Get application URLs/wait for healthy.
	externalURL := tr.Platform.AcquireAppExternalURL("resiliencyapp")
	require.NotEmpty(t, externalURL, "resiliency external URL must not be empty!")

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// Do a successful request to start to make sure our CB is cleared.
			passingMessage := createFailureMessage(nil, nil, nil)
			passingBody, _ := json.Marshal(passingMessage)
			_, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/invokeService/%s", externalURL, tc.CallType), passingBody)
			require.NoError(t, err)
			require.Equal(t, 200, code)

			failureCount := 20
			message := createFailureMessage(&failureCount, nil, nil)
			b, _ := json.Marshal(message)
			// The Circuit Breaker will trip after 15 consecutive errors each request is retried 5 times. Send the message 3 times to hit the breaker.
			for i := 0; i < 3; i++ {
				_, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/invokeService/%s", externalURL, tc.CallType), b)
				require.NoError(t, err)
				require.Equal(t, 500, code)
			}

			// Validate the call count. Circuit Breaker trips at >15, so 16 should be max.
			var callCount map[string][]CallRecord
			getCallsURL := "tests/getCallCount"
			if strings.Contains(tc.CallType, "grpc") {
				getCallsURL = "tests/getCallCountGRPC"
			}
			resp, err := utils.HTTPGet(fmt.Sprintf("%s/%s", externalURL, getCallsURL))
			require.NoError(t, err)
			err = json.Unmarshal(resp, &callCount)
			require.NoError(t, err)
			require.Equal(t, 16, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))

			// We shouldn't be able to call the app anymore.
			body, code, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/tests/invokeService/%s", externalURL, "http"), b)
			require.NoError(t, err)
			require.Equal(t, 500, code)
			require.Contains(t, string(body), "circuit breaker is open")

			// We shouldn't even see a call recorded.
			resp, err = utils.HTTPGet(fmt.Sprintf("%s/tests/getCallCount", externalURL))
			require.NoError(t, err)
			err = json.Unmarshal(resp, &callCount)
			require.NoError(t, err)
			require.Equal(t, 16, len(callCount[message.ID]), fmt.Sprintf("Call count mismatch for message %s", message.ID))
		})
	}
}

func createFailureMessage(maxFailure *int, timeout *time.Duration, failureResponseCode *int) FailureMessage {
	message := FailureMessage{
		ID: uuid.New().String(),
	}

	if maxFailure != nil {
		message.MaxFailureCount = maxFailure
	}

	if timeout != nil {
		message.Timeout = timeout
	}

	if failureResponseCode != nil {
		message.ResponseCode = failureResponseCode
	}

	return message
}
