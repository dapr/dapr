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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/utils"
)

func init() {
	suite.Register(new(placementpresence))
}

// placementpresence drives the pod informer end to end: a placement pod seen
// by the kubernetes API withholds the scheduler's placement leader, and
// deleting the pod hands placement to the scheduler.
type placementpresence struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
	kubeapi   *kubernetes.Kubernetes
	pods      *store.Store
	log       *logline.LogLine
}

func (p *placementpresence) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	tld, err := utils.GetKubeClusterDomain()
	require.NoError(t, err)
	p.sentry = sentry.New(t,
		sentry.WithTrustDomain(tld),
	)

	p.pods = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Pod"})
	p.pods.Add(&corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dapr-placement-server-0",
			Namespace: "default",
			Labels:    map[string]string{"app": "dapr-placement-server"},
		},
	})

	p.kubeapi = kubernetes.New(t,
		kubernetes.WithNamespacedPodListFromStore(t, "default", p.pods),
		kubernetes.WithClusterNamespaceList(t, &corev1.NamespaceList{
			Items: []corev1.Namespace{{
				TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}},
		}),
	)

	p.log = logline.New(t, logline.WithStdoutLineContains(
		"actor placement stays with the placement service and this scheduler withholds its placement leader",
	))
	p.scheduler = scheduler.New(t,
		scheduler.WithSentry(p.sentry),
		scheduler.WithKubeconfig(p.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
		scheduler.WithExecOptions(exec.WithStdout(p.log.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(p.log, p.sentry, p.kubeapi, p.scheduler),
	}
}

func (p *placementpresence) Run(t *testing.T, ctx context.Context) {
	p.scheduler.WaitUntilRunning(t, ctx)

	client := p.scheduler.ClientMTLS(t, ctx, "capable-sidecar")

	// A capable sidecar satisfies the gate, so only the informer's presence
	// signal withholds the leader.
	watch, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, watch.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:                      "capable-sidecar",
				Namespace:                  "default",
				SupportsSchedulerPlacement: true,
			},
		},
	}))

	leader := func() bool {
		stream, err := client.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}

	require.Never(t, leader, time.Second*10, time.Millisecond*10,
		"the informer sees a placement pod, so the leader must be withheld")

	// Deleting the placement pod hands placement to the scheduler.
	p.pods.Set()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, leader())
	}, time.Second*15, time.Millisecond*10)

	p.log.EventuallyFoundAll(t)
}
