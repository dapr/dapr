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

package hellodapr_e2e

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName            = "hellodapr"
	daprServiceName    = appName + "-dapr"
	appIDAnnotationKey = "dapr.io/app-id"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs(appName)
	utils.InitHTTPClient(true)

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:           appName,
			DaprEnabled:       true,
			ImageName:         "e2e-hellodapr",
			Replicas:          1,
			IngressEnabled:    false,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	code := tr.Start(m)

	for _, app := range testApps {
		// Teardown deletes the app's Deployment without waiting for the
		// operator to react, so the companion Service it owns can still be
		// around briefly after Start returns; poll instead of checking once.
		pollCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		waitErr := wait.PollUntilContextCancel(pollCtx, time.Second, true, func(context.Context) (bool, error) {
			_, err := tr.Platform.GetService(daprServiceName)
			if err == nil {
				return false, nil
			}
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		})
		cancel()
		if waitErr != nil {
			log.Fatalf("the dapr service %s still exists after app %s deleted (or failed to check): %v", daprServiceName, app.AppName, waitErr)
		}
	}

	os.Exit(code)
}

func TestHelloDapr(t *testing.T) {
	service, err := tr.Platform.GetService(daprServiceName)
	require.NoError(t, err)
	require.Equal(t, appName, service.Annotations[appIDAnnotationKey])
}
