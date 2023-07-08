//go:build e2e
// +build e2e

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

package healthcheck_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 60
)

func TestMain(m *testing.M) {
	utils.SetupLogs("healthcheck")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:                "healthapp-http",
			AppPort:                4000,
			AppProtocol:            "http",
			DaprEnabled:            true,
			ImageName:              "e2e-healthapp",
			Replicas:               1,
			IngressEnabled:         true,
			IngressPort:            3000,
			MetricsEnabled:         true,
			EnableAppHealthCheck:   true,
			AppHealthCheckPath:     "/healthz",
			AppHealthProbeInterval: 3,
			AppHealthProbeTimeout:  200,
			AppHealthThreshold:     3,
			DaprEnv:                `CRON_SCHEDULE="@every 1s"`, // Test envRef for components
			AppEnv: map[string]string{
				"APP_PORT":            "4000",
				"CONTROL_PORT":        "3000",
				"EXPECT_APP_PROTOCOL": "http",
			},
		},
		{
			AppName:                "healthapp-grpc",
			AppPort:                4000,
			AppProtocol:            "grpc",
			DaprEnabled:            true,
			ImageName:              "e2e-healthapp",
			Replicas:               1,
			IngressEnabled:         true,
			IngressPort:            3000,
			MetricsEnabled:         true,
			EnableAppHealthCheck:   true,
			AppHealthCheckPath:     "/healthz",
			AppHealthProbeInterval: 3,
			AppHealthProbeTimeout:  200,
			AppHealthThreshold:     3,
			DaprEnv:                `CRON_SCHEDULE="@every 1s"`,
			AppEnv: map[string]string{
				"APP_PORT":            "4000",
				"CONTROL_PORT":        "3000",
				"EXPECT_APP_PROTOCOL": "grpc",
			},
		},
		{
			AppName:                "healthapp-h2c",
			AppPort:                4000,
			AppProtocol:            "h2c",
			DaprEnabled:            true,
			ImageName:              "e2e-healthapp",
			Replicas:               1,
			IngressEnabled:         true,
			IngressPort:            3000,
			MetricsEnabled:         true,
			EnableAppHealthCheck:   true,
			AppHealthCheckPath:     "/healthz",
			AppHealthProbeInterval: 3,
			AppHealthProbeTimeout:  200,
			AppHealthThreshold:     3,
			DaprEnv:                `CRON_SCHEDULE="@every 1s"`,
			AppEnv: map[string]string{
				"APP_PORT":            "4000",
				"CONTROL_PORT":        "3000",
				"EXPECT_APP_PROTOCOL": "h2c",
			},
		},
	}

	log.Print("Creating TestRunner")
	tr = runner.NewTestRunner("healthchecktest", testApps, nil, nil)
	log.Print("Starting TestRunner")
	os.Exit(tr.Start(m))
}

func TestAppHealthCheckHTTP(t *testing.T) {
	testAppHealthCheckProtocol(t, "http")
}

func TestAppHealthCheckGRPC(t *testing.T) {
	testAppHealthCheckProtocol(t, "grpc")
}

func TestAppHealthCheckH2C(t *testing.T) {
	testAppHealthCheckProtocol(t, "h2c")
}

func testAppHealthCheckProtocol(t *testing.T, protocol string) {
	appName := "healthapp-" + protocol
	appExternalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, appExternalURL, "appExternalURL must not be empty!")

	if !strings.HasPrefix(appExternalURL, "http://") && !strings.HasPrefix(appExternalURL, "https://") {
		appExternalURL = "http://" + appExternalURL
	}
	appExternalURL = strings.TrimSuffix(appExternalURL, "/")

	log.Println("App external URL", appExternalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(appExternalURL, numHealthChecks)
	require.NoError(t, err)

	getCountAndLast := func(t *testing.T, method string) (obj countAndLast) {
		u := appExternalURL + "/" + method
		log.Println("Invoking method", "GET", u)

		res, err := utils.HTTPGet(u)
		require.NoError(t, err)

		err = json.Unmarshal(res, &obj)
		require.NoError(t, err)
		assert.NotEmpty(t, obj.Count)

		return obj
	}

	invokeFoo := func(t *testing.T) (res []byte, status int) {
		u := fmt.Sprintf("%s/invoke/%s/foo", appExternalURL, appName)
		log.Println("Invoking method", "POST", u)

		res, status, err := utils.HTTPPostWithStatus(u, []byte{})
		require.NoError(t, err)

		return res, status
	}

	t.Run("last health check within 3s", func(t *testing.T) {
		obj := getCountAndLast(t, "last-health-check")
		_ = assert.NotNil(t, obj.Last) &&
			assert.Less(t, *obj.Last, int64(3500)) // Adds .5s to reduce flakiness on slow runners
	})

	t.Run("invoke method should work on healthy app", func(t *testing.T) {
		res, status := invokeFoo(t)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "🤗", string(res))
	})

	t.Run("last input binding within 1s", func(t *testing.T) {
		obj := getCountAndLast(t, "last-input-binding")
		_ = assert.NotNil(t, obj.Last) &&
			assert.Less(t, *obj.Last, int64(1500)) // Adds .5s to reduce flakiness on slow runners
	})

	t.Run("last topic message within 1s", func(t *testing.T) {
		obj := getCountAndLast(t, "last-topic-message")
		_ = assert.NotNil(t, obj.Last) &&
			assert.Less(t, *obj.Last, int64(1500)) // Adds .5s to reduce flakiness on slow runners
	})

	t.Run("set plan and trigger failures", func(t *testing.T) {
		u := fmt.Sprintf("%s/set-plan", appExternalURL)
		log.Println("Invoking method", "POST", u)
		plan, _ := json.Marshal([]bool{false, false, false, false, false, false})
		_, err := utils.HTTPPost(u, plan)
		require.NoError(t, err)

		// We will need to wait for 11 seconds (3 failed probes at 3 seconds interval + 2s buffer)
		wait := time.After(11 * time.Second)

		// Get the last counts
		var (
			lastInputBinding countAndLast
			lastTopicMessage countAndLast
			lastHealthCheck  countAndLast
		)
		wg := sync.WaitGroup{}

		t.Run("retrieve counts after failures", func(t *testing.T) {
			wg.Add(3)
			go func() {
				lastInputBinding = getCountAndLast(t, "last-input-binding")
				wg.Done()
			}()
			go func() {
				lastTopicMessage = getCountAndLast(t, "last-topic-message")
				wg.Done()
			}()
			go func() {
				lastHealthCheck = getCountAndLast(t, "last-health-check")
				wg.Done()
			}()
			wg.Wait()

			// Ensure we have the result of last-input-binding and last-health-check
			require.NotEmpty(t, lastInputBinding.Count)
			require.NotEmpty(t, lastHealthCheck.Count)
		})

		t.Run("after delay, get updated counters and service invocation fails", func(t *testing.T) {
			// Wait for the remainder of the time
			<-wait

			// Get the last values
			wg.Add(4)
			go func() {
				obj := getCountAndLast(t, "last-input-binding")
				require.Greater(t, obj.Count, lastInputBinding.Count)
				lastInputBinding = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-topic-message")
				require.Greater(t, obj.Count, lastTopicMessage.Count)
				lastTopicMessage = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-health-check")
				require.Greater(t, obj.Count, lastHealthCheck.Count)
				lastHealthCheck = obj
				wg.Done()
			}()
			// Service invocation should fail
			go func() {
				res, status := invokeFoo(t)
				require.Contains(t, string(res), "ERR_DIRECT_INVOKE")
				require.Greater(t, status, 299)
				wg.Done()
			}()
			wg.Wait()
		})

		t.Run("after delay, counters aren't increasing", func(t *testing.T) {
			// Wait 5 seconds then repeat, expecting the same results for counters
			time.Sleep(5 * time.Second)
			wg.Add(4)
			go func() {
				obj := getCountAndLast(t, "last-input-binding")
				require.Equal(t, lastInputBinding.Count, obj.Count)
				require.Greater(t, *obj.Last, int64(5000))
				lastInputBinding = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-topic-message")
				require.Equal(t, lastTopicMessage.Count, obj.Count)
				require.Greater(t, *obj.Last, int64(5000))
				lastTopicMessage = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-health-check")
				require.Greater(t, obj.Count, lastHealthCheck.Count)
				require.Less(t, *obj.Last, int64(3000))
				lastHealthCheck = obj
				wg.Done()
			}()
			// Service invocation should fail again
			go func() {
				res, status := invokeFoo(t)
				require.Greater(t, status, 299)
				require.Contains(t, string(res), "ERR_DIRECT_INVOKE")
				wg.Done()
			}()
			wg.Wait()
		})

		t.Run("app resumes after health probes pass", func(t *testing.T) {
			// Wait another 12 seconds, when everything should have resumed
			time.Sleep(12 * time.Second)
			wg.Add(4)
			go func() {
				obj := getCountAndLast(t, "last-input-binding")
				require.Greater(t, obj.Count, lastInputBinding.Count)
				require.Less(t, *obj.Last, int64(1500)) // Adds .5s to reduce flakiness on slow runners
				lastInputBinding = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-topic-message")
				require.Greater(t, obj.Count, lastTopicMessage.Count)
				require.Less(t, *obj.Last, int64(1500)) // Adds .5s to reduce flakiness on slow runners
				lastTopicMessage = obj
				wg.Done()
			}()
			go func() {
				obj := getCountAndLast(t, "last-health-check")
				require.Greater(t, obj.Count, lastHealthCheck.Count)
				require.Less(t, *obj.Last, int64(3500)) // Adds .5s to reduce flakiness on slow runners
				lastHealthCheck = obj
				wg.Done()
			}()
			// Service invocation works
			go func() {
				res, status := invokeFoo(t)
				require.Equal(t, 200, status)
				require.Equal(t, "🤗", string(res))
				wg.Done()
			}()
			wg.Wait()
		})
	})
}

type countAndLast struct {
	// Total number of actions
	Count int64 `json:"count"`
	// Time since last action, in ms
	Last *int64 `json:"last"`
}
