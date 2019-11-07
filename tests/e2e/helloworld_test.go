// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"testing"

	"github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/utils"
	"github.com/stretchr/testify/assert"
)

func TestHelloWorld(t *testing.T) {
	// TODO: kubeconfig needs to be configurable
	kubeClient, err := kubernetes.NewKubeClient("", "")
	assert.NoError(t, err)
	appManager := kubernetes.NewAppManager(kubeClient, kubernetes.DaprTestKubeNameSpace)

	testApp := utils.AppDescription{
		AppName:        "helloword",
		DaprEnabled:    true,
		ImageName:      "e2e-helloworld",
		RegistryName:   "darpiotest",
		Replicas:       1,
		IngressEnabled: true,
	}

	defer appManager.Cleanup(testApp)

	_, err = appManager.Deploy(testApp)
	assert.NoError(t, err)

	_, err = appManager.WaitUntilDeploymentIsDone(testApp)
	assert.NoError(t, err)

	// TODO: run test
}
