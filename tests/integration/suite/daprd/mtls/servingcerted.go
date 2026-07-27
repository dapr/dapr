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

package mtls

import (
	"context"
	"strconv"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(servingcerted))
}

type servingcerted struct {
	sentry *procsentry.Sentry
	daprd  *daprd.Daprd
}

func (s *servingcerted) Setup(t *testing.T) []framework.Option {
	s.sentry = procsentry.New(t)
	s.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(s.sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(s.sentry.Address()),
		daprd.WithEnableMTLS(true),
	)
	return []framework.Option{
		framework.WithProcesses(s.sentry, s.daprd),
	}
}

func (s *servingcerted) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	cert.AssertEd25519ServingCert(t, ctx, "localhost:"+strconv.Itoa(s.daprd.InternalGRPCPort()))
}
