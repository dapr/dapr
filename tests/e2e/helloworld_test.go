// +build e2e
// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"log"
	"testing"

	"github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/utils"
	"github.com/stretchr/testify/require"
)

// NOTE NOTE!! WOKRING IN PROGRESS.
// This is the example to show how AppManager is used for the e2e tests.
// The subsequent PRs will introduce test framework which encapsulates deployment
// and app varification steps.

func TestHelloWorld(t *testing.T) {
	kubeClient, err := kubernetes.NewKubeClient("", "")
	require.NoError(t, err)

	appManager := kubernetes.NewAppManager(kubeClient, kubernetes.DaprTestKubeNameSpace)

	// Test App description
	testApp := utils.AppDescription{
		AppName:        "helloword",
		DaprEnabled:    true,
		ImageName:      "e2e-helloworld",
		RegistryName:   "youngp",
		Replicas:       1,
		IngressEnabled: true,
	}

	// Clean up app and services before starting test
	appManager.Cleanup(testApp)

	// Tear down when the test is completed
	defer appManager.Cleanup(testApp)

	// Deploy app and wait until deployment is done
	_, err = appManager.Deploy(testApp)
	require.NoError(t, err)
	_, err = appManager.WaitUntilDeploymentState(testApp, appManager.IsDeploymentDone)
	require.NoError(t, err)

	// Validate daprd side car is injected
	ok, err := appManager.ValidiateSideCar(testApp)
	require.NoError(t, err)
	require.True(t, ok)

	// Create Ingress endpoint
	_, err = appManager.CreateIngressService(testApp)
	require.NoError(t, err)

	// Wait until external ip is assigned
	svc, err := appManager.WaitUntilServiceState(testApp, appManager.IsServiceIngressReady)
	require.NoError(t, err)

	// Get external url
	externalURL := appManager.AcquireExternalURLFromService(svc)

	// Begin tests
	log.Printf("%s service external url: %s", testApp.AppName, externalURL)

	// TODO: run test
}
