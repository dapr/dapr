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

package kubernetes

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/utils"
)

func init() {
	suite.Register(new(retry))
}

type retry struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
	kubeapi   *kubernetes.Kubernetes
	logline   *logline.LogLine
	ln        net.Listener
	ln6       net.Listener
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	tld, err := utils.GetKubeClusterDomain()
	require.NoError(t, err)

	r.sentry = sentry.New(t,
		sentry.WithTrustDomain(tld),
	)

	r.kubeapi = kubernetes.New(t,
		kubernetes.WithClusterNamespaceList(t, &corev1.NamespaceList{
			Items: []corev1.Namespace{{
				TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}},
		}),
	)

	fp := ports.Reserve(t, 1)
	r.ln = fp.Listener(t)
	port := r.ln.Addr().(*net.TCPAddr).Port
	if ln6, lerr := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); lerr == nil {
		r.ln6 = ln6
	}
	t.Cleanup(r.closeListeners)

	r.logline = logline.New(t, logline.WithStdoutLineContains(
		"Scheduler server failed, recreating in",
	))

	r.scheduler = scheduler.New(t,
		scheduler.WithSentry(r.sentry),
		scheduler.WithKubeconfig(r.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithEtcdClientPort(port),
		scheduler.WithLogLineStdout(r.logline),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.kubeapi, r.logline, r.scheduler),
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.sentry.WaitUntilRunning(t, ctx)
	r.logline.EventuallyFoundAll(t)

	r.closeListeners()
	r.scheduler.WaitUntilRunning(t, ctx)

	client := r.scheduler.ClientMTLS(t, ctx, "myapp")
	_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: new("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)

	etcdClient := r.scheduler.ETCDClient(t, ctx).KV
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		require.NoError(c, gerr)
		assert.Len(c, resp.Kvs, 1)
	}, time.Second*10, 10*time.Millisecond)
}

func (r *retry) closeListeners() {
	if r.ln != nil {
		r.ln.Close()
		r.ln = nil
	}
	if r.ln6 != nil {
		r.ln6.Close()
		r.ln6 = nil
	}
}
