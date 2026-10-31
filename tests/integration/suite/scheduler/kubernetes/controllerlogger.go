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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/utils"
)

func init() {
	suite.Register(new(controllerlogger))
}

// controllerlogger ensures controller-runtime does not print its
// "log.SetLogger(...) was never called" stack trace, which it does when its
// logger is first used 30s or more after process start without being set.
type controllerlogger struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
	kubeapi   *kubernetes.Kubernetes
	logline   *logline.LogLine
}

// Timeout extends the default case timeout, as the test has to wait for 30s.
func (c *controllerlogger) Timeout() time.Duration {
	return time.Second * 90
}

func (c *controllerlogger) Setup(t *testing.T) []framework.Option {
	// Skip windows as test requires a resolvconf lookup.
	os.SkipWindows(t)

	tld, err := utils.GetKubeClusterDomain()
	require.NoError(t, err)

	c.sentry = sentry.New(t,
		sentry.WithTrustDomain(tld),
	)

	c.kubeapi = kubernetes.New(t,
		kubernetes.WithNamespacedPodList(t, "default", &corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}}),
		kubernetes.WithClusterNamespaceList(t, &corev1.NamespaceList{
			Items: []corev1.Namespace{{
				TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}},
		}),
	)

	c.logline = logline.New(t, logline.WithCaptureAll())

	c.scheduler = scheduler.New(t,
		scheduler.WithSentry(c.sentry),
		scheduler.WithKubeconfig(c.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithExecOptions(
			exec.WithStdout(c.logline.Stdout()),
			exec.WithStderr(c.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.logline, c.sentry, c.kubeapi, c.scheduler),
	}
}

func (c *controllerlogger) Run(t *testing.T, ctx context.Context) {
	// The scheduler process has started by the time Run is called.
	started := time.Now()

	c.sentry.WaitUntilRunning(t, ctx)
	c.scheduler.WaitUntilRunning(t, ctx)

	client := c.scheduler.ClientMTLS(t, ctx, "myapp")
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

	etcdClient := c.scheduler.ETCDClient(t, ctx).KV
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, err := etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		require.NoError(col, err)
		assert.Len(col, resp.Kvs, 1)
	}, time.Second*10, 10*time.Millisecond)

	// controller-runtime only prints the message once its logger is used 30s
	// or more after process start.
	select {
	case <-time.After(time.Until(started.Add(time.Second * 31))):
	case <-ctx.Done():
		require.Fail(t, "context done before 30s elapsed")
	}

	// Deleting the namespace makes the namespaces controller reconcile, which
	// uses the controller-runtime logger.
	c.kubeapi.Informer().DeleteWait(t, ctx, &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
	})

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, err := etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		require.NoError(col, err)
		assert.Empty(col, resp.Kvs)
	}, time.Second*5, 10*time.Millisecond)

	// Stop the scheduler and wait for its output to be fully read before
	// checking it.
	c.scheduler.Cleanup(t)
	c.logline.EventuallyFoundAll(t)
	assert.False(t, c.logline.Contains("log.SetLogger(...) was never called"))
}
