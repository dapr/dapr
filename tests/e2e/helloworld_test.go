// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"testing"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/utils"
	"github.com/stretchr/testify/require"
)

var runner *utils.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed for helloworld test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "helloworld",
			DaprEnabled:    true,
			ImageName:      "e2e-helloworld",
			RegistryName:   "youngp",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        "helloworld-1",
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
	resp, _ := httpGet(externalURL)
	require.Equal(t, resp, []byte("Hello, Dapr"))
}
