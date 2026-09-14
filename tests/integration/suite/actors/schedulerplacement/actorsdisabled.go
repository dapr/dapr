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

package schedulerplacement

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fhttp "github.com/dapr/dapr/tests/integration/framework/client"
	fos "github.com/dapr/dapr/tests/integration/framework/os"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorsdisabled))
}

// actorsdisabled reproduces k8s with actors globally disabled: the
// injector clears the actors service but still sets the scheduler address.
// A scheduler address alone must not enable actors, so the runtime stays
// DISABLED and an actor call fails rather than hanging.
type actorsdisabled struct {
	daprd *procdaprd.Daprd
}

func (a *actorsdisabled) Setup(t *testing.T) []framework.Option {
	fos.SkipWindows(t)

	sen := sentry.New(t)
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, sen.CABundle().X509.TrustAnchors, 0o600))

	op := operator.New(t, operator.WithSentry(sen))
	sched := scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	// k8s with actors disabled: a scheduler address, no actors
	// service and no placement address, as the injector produces it.
	a.daprd = procdaprd.New(t,
		procdaprd.WithAppID("my-app"),
		procdaprd.WithMode("kubernetes"),
		procdaprd.WithSentryAddress(sen.Address()),
		procdaprd.WithControlPlaneAddress(op.Address(t)),
		procdaprd.WithDisableK8sSecretStore(true),
		procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors))),
		procdaprd.WithSchedulerAddresses(sched.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sen, op, sched, a.daprd),
	}
}

func (a *actorsdisabled) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, rtv1.ActorRuntime_DISABLED, meta.GetActorRuntime().GetRuntimeStatus())
	}, time.Second*20, time.Millisecond*10)

	url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactorid/method/foo", a.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := fhttp.HTTP(t).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "ERR_ACTOR_NO_PLACEMENT")
}
