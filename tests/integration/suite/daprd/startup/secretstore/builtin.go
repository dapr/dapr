/*
Copyright 2026 The Dapr Authors
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

package secretstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(builtin))
}

type builtin struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logline  *logline.LogLine
}

func (b *builtin) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	b.operator = operator.New(t,
		operator.WithSentry(sentry),
	)

	b.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Failed to init component kubernetes (secretstores.kubernetes/v1)",
			"Error processing component, daprd will exit gracefully: process component kubernetes error",
		),
	)

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kubeconfig, []byte("not a valid kubeconfig"), 0o600))

	b.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
				"KUBECONFIG", kubeconfig,
			),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(b.operator.Address(t)),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(b.logline),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, b.operator, b.logline, b.daprd),
	}
}

func (b *builtin) Run(t *testing.T, ctx context.Context) {
	b.logline.EventuallyFoundAll(t)
	b.daprd.WaitUntilExit(t, time.Second*30)
}
