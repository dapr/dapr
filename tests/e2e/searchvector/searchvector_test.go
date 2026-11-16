//go:build e2e

/*
Copyright 2026 The Dapr Authors
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

package searchvector_e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName         = "searchvectorapp"
	numHealthChecks = 60
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("searchvector")
	utils.InitHTTPClient(true)

	// Meilisearch is installed by `make setup-test-env-meilisearch`.
	meilisearch := map[string]kube.MetadataValue{
		"host":   {Raw: `"http://dapr-meilisearch:7700"`},
		"apiKey": {Raw: `"e2e-master-key"`},
	}
	comps := []kube.ComponentDescription{
		{Name: "e2e-search", TypeName: "search.meilisearch", MetaData: meilisearch, Scopes: []string{appName}},
		{Name: "e2e-vector", TypeName: "vector.meilisearch", MetaData: meilisearch, Scopes: []string{appName}},
	}
	apps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-searchvector",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, apps, comps, nil)
	os.Exit(tr.Start(m))
}

func TestSearchVector(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Each scenario creates, exercises and deletes its own index or
	// collection, and reports the first step that misbehaves.
	for _, scenario := range []string{"search", "vector"} {
		t.Run(scenario, func(t *testing.T) {
			body, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("%s/test/%s/%s", externalURL, scenario, uuid.NewString()), nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, string(body))
		})
	}
}
