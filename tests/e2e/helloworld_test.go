// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"os"
	"testing"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed for helloworld test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "hellodapr",
			DaprEnabled:    true,
			ImageName:      "e2e-helloworld",
			RegistryName:   "youngp",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        "hellodapr1",
			DaprEnabled:    true,
			ImageName:      "e2e-helloworld",
			RegistryName:   "youngp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("helloworld", testApps)
	os.Exit(tr.Start(m))
}

func TestHelloDaprApp(t *testing.T) {
	// Get Ingress external url for "hellodapr" test app
	externalURL := tr.Platform.AcquireAppExternalURL("hellodapr")
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Call endpoint for "hellodapr" test app
	resp, _ := httpGet(externalURL)
	require.Equal(t, resp, []byte("Hello, Dapr"))
}

func TestHelloDapr1App(t *testing.T) {
	// Get Ingress external url for "hellodapr1" test app
	externalURL := tr.Platform.AcquireAppExternalURL("hellodapr1")
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Call endpoint for "hellodapr1" test app
	resp, _ := httpGet(externalURL)
	require.Equal(t, resp, []byte("Hello, Dapr"))
}
