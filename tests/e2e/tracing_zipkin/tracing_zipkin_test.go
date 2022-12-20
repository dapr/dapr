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

package tracing_zipkin_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

type appResponse struct {
	Message  string `json:"message,omitempty"`
	SpanName string `json:"spanName,omitempty"`
}

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("tracing")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:           "tracingapp-a",
			DaprEnabled:       true,
			ImageName:         "e2e-tracingapp",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
			Config:            "tracingconfig-zipkin",
		},
		{
			AppName:           "tracingapp-b",
			DaprEnabled:       true,
			ImageName:         "e2e-tracingapp",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
			Config:            "tracingconfig-zipkin",
		},
	}

	tr = runner.NewTestRunner("tracing", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestInvoke(t *testing.T) {
	t.Run("Simple HTTP invoke with tracing", func(t *testing.T) {
		// Get the ingress external url of test app
		externalURL := tr.Platform.AcquireAppExternalURL("tracingapp-a")
		require.NotEmpty(t, externalURL, "external URL must not be empty")

		// Check if test app endpoint is available
		_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
		require.NoError(t, err)

		// Get the ingress external url of test app 2
		externalURL2 := tr.Platform.AcquireAppExternalURL("tracingapp-b")
		require.NotEmpty(t, externalURL, "external URL must not be empty")

		// Check if test app 2 endpoint is available
		_, err = utils.HTTPGetNTimes(externalURL2, numHealthChecks)
		require.NoError(t, err)

		// Trigger test
		resp, err := utils.HTTPPost(externalURL+"/triggerInvoke?appId=tracingapp-b", nil)
		require.NoError(t, err)

		var appResp appResponse
		err = json.Unmarshal(resp, &appResp)
		require.NoError(t, err)
		require.Equal(t, "OK", appResp.Message)

		// Validate tracers
		spanName := appResp.SpanName
		err = backoff.Retry(func() error {
			respV, errV := utils.HTTPPost(externalURL+"/validate?spanName="+spanName, nil)
			if errV != nil {
				return errV
			}

			log.Printf("Response from validate: %s", string(respV))
			var appRespV appResponse
			errV = json.Unmarshal(respV, &appRespV)
			if errV != nil {
				return fmt.Errorf("error parsing appResponse: %v", errV)
			}

			if appRespV.Message != "OK" {
				return fmt.Errorf("tracers validation failed")
			}

			return nil
		}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
		require.NoError(t, err)
	})
}
