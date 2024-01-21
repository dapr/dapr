/*
Copyright 2023 The Dapr Authors
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

package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(cli))
}

// cli tests daprd's response to metadata authorization CLI/config options.
type cli struct {
	logline *logline.LogLine
}

func (c *cli) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	c.logline = logline.New(t,
		logline.WithStdoutLineContains(
			`invalid SPIFFE ID \"foobar\" in default metadata authorized IDs: scheme is missing or invalid`,
			`invalid SPIFFE ID \"hello-world\" in default metadata authorized IDs: scheme is missing or invalid`,
		),
	)

	daprd := daprd.New(t,
		daprd.WithExecOptions(),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(c.logline.Stdout()),
			exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors)),
		),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithMetadataAuthorizedIDs(
			"foobar",
		),
		daprd.WithConfigContents(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: metadata
spec:
  mtls:
    metadataAuthorizedIDs: ["hello-world"]
`))

	return []framework.Option{
		framework.WithProcesses(sentry, c.logline, daprd),
	}
}

func (c *cli) Run(t *testing.T, _ context.Context) {
	c.logline.EventuallyFoundAll(t)
}
