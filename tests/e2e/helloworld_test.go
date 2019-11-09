// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"log"
	"testing"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/utils"
)

var runner *utils.TestRunner

func TestMain(m *testing.M) {
	runner = utils.NewTestRunner("helloworld")

	runner.RegisterTestApps([]kube.AppDescription{
		{
			AppName:        "helloworld",
			DaprEnabled:    true,
			ImageName:      "e2e-helloworld",
			RegistryName:   "youngp",
			Replicas:       1,
			IngressEnabled: true,
		},
	})

	runner.Start(m)
}

func TestHelloWorld(t *testing.T) {
	ingressURL := runner.AcquireAppExternalURL("helloworld")

	log.Printf("service external url: %s", ingressURL)
}
