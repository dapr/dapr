// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"testing"

	"github.com/dapr/dapr/tests/utils/kubernetes"
	"github.com/stretchr/testify/assert"
)

func TestHelloWorld(t *testing.T) {
	// TODO: kubeconfig needs to be configurable
	kubeClient, err := kubernetes.NewKubeClient("", "")
	assert.NoError(t, err)
	appUtils := kubernetes.NewAppUtils(kubeClient, kubernetes.DaprTestKubeNameSpace)

	testApp := kubernetes.AppDescription{
		AppName:        "helloword",
		DaprEnabled:    true,
		ImageName:      "e2e-helloworld",
		RegistryName:   "darpiotest",
		Replicas:       1,
		IngressEnabled: true,
	}

	defer appUtils.CleanupApp(testApp)

	err = appUtils.DeployApp(testApp)
	assert.NoError(t, err)

	_, err = appUtils.WaitUntilDeploymentReady(testApp)
	assert.NoError(t, err)

	// TODO: run test
}
