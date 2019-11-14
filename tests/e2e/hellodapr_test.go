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
	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "hellodapr",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        "hellobluedapr",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("hellodapr", testApps)
	os.Exit(tr.Start(m))
}

func TestHelloDapr(t *testing.T) {
	// Get Ingress external url for "hellodapr" test app
	externalURL := tr.Platform.AcquireAppExternalURL("hellodapr")
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Call endpoint for "hellodapr" test app
	resp, _ := httpGet(externalURL)
	require.Equal(t, resp, []byte("Hello, Dapr"))
}

func TestHelloBlueDapr(t *testing.T) {
	// Get Ingress external url for "hellobluedapr" test app
	externalURL := tr.Platform.AcquireAppExternalURL("hellobluedapr")
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Call endpoint for "hellobluedapr" test app
	resp, _ := httpGet(externalURL)
	require.Equal(t, resp, []byte("Hello, Dapr"))
}
