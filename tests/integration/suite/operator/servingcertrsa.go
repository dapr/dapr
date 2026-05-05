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

package operator

import (
	"context"
	"strconv"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procoperator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(servingcertrsa))
}

type servingcertrsa struct {
	sentry   *procsentry.Sentry
	kubeapi  *kubernetes.Kubernetes
	operator *procoperator.Operator
}

func (s *servingcertrsa) Setup(t *testing.T) []framework.Option {
	s.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			s.sentry.Port(),
		),
	)

	s.operator = procoperator.New(t,
		procoperator.WithNamespace("default"),
		procoperator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		procoperator.WithTrustAnchorsFile(s.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(s.kubeapi, s.sentry, s.operator),
	}
}

func (s *servingcertrsa) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.operator.WaitUntilRunning(t, ctx)
	cert.AssertRSAServingCert(t, ctx, "localhost:"+strconv.Itoa(s.operator.WebhookPort()))
}
