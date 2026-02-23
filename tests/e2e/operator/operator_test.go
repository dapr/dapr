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
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName            = "hellodapr"
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

	// Verify that the Dapr service is cleaned up after the app is deleted
	// The Dapr service name is based on the formatted app-id: <formatted-app-name>-dapr
	formattedDaprServiceName := kube.FormatAppID(appName) + "-dapr"
	for _, app := range testApps {
		_, err := tr.Platform.GetService(formattedDaprServiceName)
		if err == nil {
			log.Fatalf("the dapr service %s still exists after app %s deleted", formattedDaprServiceName, app.AppName)
		} else if !errors.IsNotFound(err) {
			log.Fatalf("failed to get dapr service %s, err: %v", formattedDaprServiceName, err)
		}
	}

	os.Exit(code)
}

func TestHelloDapr(t *testing.T) {
	// The Dapr service name is based on the formatted app-id: <formatted-app-name>-dapr
	formattedDaprServiceName := kube.FormatAppID(appName) + "-dapr"
	service, err := tr.Platform.GetService(formattedDaprServiceName)
	require.NoError(t, err)
	// The app-id annotation should also be the formatted name
	require.Equal(t, kube.FormatAppID(appName), service.Annotations[appIDAnnotationKey])
}
