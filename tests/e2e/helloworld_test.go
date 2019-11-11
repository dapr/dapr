// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/utils"
	"github.com/stretchr/testify/require"
)

var runner *utils.TestRunner

func TestMain(m *testing.M) {
	testApps := []kube.AppDescription{
		{
			AppName:        "helloworld",
			DaprEnabled:    true,
			ImageName:      "e2e-helloworld",
			RegistryName:   "youngp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	runner = utils.NewTestRunner("helloworld", testApps)

	runner.Start(m)
}

func TestHelloWorld(t *testing.T) {
	// Get Ingress external url for "helloworld" test app
	externalURL := runner.Platform.AcquireAppExternalURL("helloworld")
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Call endpoint for "helloworld" test app
	resp, err := http.Get(fmt.Sprintf("http://%s", externalURL))
	require.NoError(t, err)
	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)

	require.Equal(t, body, []byte("Hello, Dapr"))
}
