/*
Copyright 2024 The Dapr Authors
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
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(prefix))
}

type prefix struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
	kubeapi   *kubernetes.Kubernetes
}

func (p *prefix) Setup(t *testing.T) []framework.Option {
	p.sentry = sentry.New(t)

	p.kubeapi = kubernetes.New(t,
		kubernetes.WithClusterNamespaceList(t, &corev1.NamespaceList{
			Items: []corev1.Namespace{{
				TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}},
		}),
	)

	p.scheduler = scheduler.New(t,
		scheduler.WithSentry(p.sentry),
		scheduler.WithKubeconfig(p.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	return []framework.Option{
		framework.WithProcesses(p.sentry, p.kubeapi, p.scheduler),
	}
}

func (p *prefix) Run(t *testing.T, ctx context.Context) {
	p.sentry.WaitUntilRunning(t, ctx)
	p.scheduler.WaitUntilRunning(t, ctx)

	client1 := p.scheduler.ClientMTLSNS(t, ctx, "default", "myapp")
	client2 := p.scheduler.ClientMTLSNS(t, ctx, "default-foo", "myapp")
	client3 := p.scheduler.ClientMTLSNS(t, ctx, "def", "myapp")

	_, err := client1.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
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
	_, err = client2.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "default-foo",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = client3.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "def",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = client1.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "test", Type: "test"},
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = client2.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "default-foo",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "test", Type: "test"},
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = client3.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@daily")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "myapp",
			Namespace: "def",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "test", Type: "test"},
				},
			},
		},
	})
	require.NoError(t, err)

	etcdClient := p.scheduler.ETCDClient(t, ctx).KV
	var resp *clientv3.GetResponse
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		require.NoError(t, err)
		assert.Len(c, resp.Kvs, 6)
	}, time.Second*10, 10*time.Millisecond)

	p.kubeapi.Informer().DeleteWait(t, ctx, &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
		require.NoError(t, err)
		assert.Len(c, resp.Kvs, 4)
	}, time.Second*10, 10*time.Millisecond)

	resp, err = etcdClient.Get(ctx, "dapr/jobs/", clientv3.WithPrefix())
	require.NoError(t, err)

	names := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		names = append(names, string(kv.Key))
	}
	assert.ElementsMatch(t, []string{
		"dapr/jobs/actorreminder||default-foo||test||test||testJob",
		"dapr/jobs/app||default-foo||myapp||testJob",
		"dapr/jobs/actorreminder||def||test||test||testJob",
		"dapr/jobs/app||def||myapp||testJob",
	}, names)
}
