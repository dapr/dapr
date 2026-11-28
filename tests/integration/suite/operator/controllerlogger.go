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
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(controllerlogger))
}

// controllerlogger ensures controller-runtime does not print its
// "log.SetLogger(...) was never called" stack trace, which it does when its
// logger is first used 30s or more after process start without being set.
type controllerlogger struct {
	sentry   *sentry.Sentry
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	logline  *logline.LogLine
}

// Timeout extends the default case timeout, as the test has to wait for 30s
// and the Deployment watch can take up to a further 30s to reconnect.
func (c *controllerlogger) Timeout() time.Duration {
	return time.Minute * 2
}

func (c *controllerlogger) Setup(t *testing.T) []framework.Option {
	c.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	c.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			c.sentry.Port(),
		),
	)

	c.logline = logline.New(t, logline.WithCaptureAll())

	c.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(c.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(c.sentry.TrustAnchorsFile(t)),
		operator.WithLogLevel("debug"),
		operator.WithExecOptions(
			exec.WithStdout(c.logline.Stdout()),
			exec.WithStderr(c.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.logline, c.kubeapi, c.sentry, c.operator),
	}
}

func (c *controllerlogger) Run(t *testing.T, ctx context.Context) {
	// The operator process has started by the time Run is called.
	started := time.Now()

	c.sentry.WaitUntilRunning(t, ctx)
	c.operator.WaitUntilRunning(t, ctx)

	// controller-runtime only prints the message once its logger is used 30s
	// or more after process start.
	select {
	case <-time.After(time.Until(started.Add(time.Second * 31))):
	case <-ctx.Done():
		require.Fail(t, "context done before 30s elapsed")
	}

	// Adding and deleting a Deployment makes the Deployment controller
	// reconcile, which uses the controller-runtime logger.
	deploy := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
	}
	c.kubeapi.Informer().Add(t, deploy)
	c.kubeapi.Informer().DeleteWait(t, ctx, deploy)
	// The operator cache coalesces Deployments without Dapr annotations into a
	// single placeholder, so the reconciled name is not "myapp".
	c.logline.EventuallyContains(t, "deployment has be deleted", time.Second*10, time.Millisecond*10)

	// Stop the operator and wait for its output to be fully read before
	// checking it.
	c.operator.Cleanup(t)
	c.logline.EventuallyFoundAll(t)
	assert.False(t, c.logline.Contains("log.SetLogger(...) was never called"))
}
