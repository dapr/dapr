/*
Copyright 2025 The Dapr Authors
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

package usergroup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(disable))
}

type disable struct {
	sentry  *sentry.Sentry
	logline *logline.LogLine
}

func (d *disable) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    nil,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: "integration.test.dapr.io",
	})
	require.NoError(t, err)
	bundle := bundle.Bundle{
		X509: x509bundle,
		JWT:  jwtbundle,
	}

	kubeAPI := utils.KubeAPI(t, utils.KubeAPIOptions{
		Bundle:         bundle,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
	})

	d.logline = logline.New(t, logline.WithStdoutLineContains(
		"Dapr must be run as a non-root user 65532:65532 in Kubernetes environments.",
	))

	d.sentry = sentry.New(t,
		sentry.WithWriteConfig(false),
		sentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		sentry.WithNamespace("sentrynamespace"),
		sentry.WithMode(string(modes.KubernetesMode)),
		sentry.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				assert.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithEnvVars(t,
				"KUBERNETES_SERVICE_HOST", "anything",
				"DAPR_UNSAFE_SKIP_CONTAINER_UID_GID_CHECK", "false",
			),
			exec.WithStdout(d.logline.Stdout()),
		),
		sentry.WithCABundle(bundle),
		sentry.WithTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, d.sentry, kubeAPI),
	}
}

func (d *disable) Run(t *testing.T, ctx context.Context) {
	d.logline.EventuallyFoundAll(t)
}
