// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_invocation_e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	actorInvokeURLFormat = "%s/test/testactorfeatures/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	numHealthChecks      = 60
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "actorinvocationservice",
			DaprEnabled:    true,
			ImageName:      "e2e-actorfeatures",
			Replicas:       2,
			IngressEnabled: false,
		},
		{
			AppName:        "actortestclient",
			DaprEnabled:    true,
			ImageName:      "e2e-actorclientapp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("actor-invocation", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorInvocation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("actortestclient")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Run("Actor remote invocation", func(t *testing.T) {
		actorID := guuid.New().String()

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "testmethod"), []byte{})
		require.NoError(t, err)
	})
}
