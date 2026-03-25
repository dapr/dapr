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

package cron

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type fakeEtcd struct {
	client *clientv3.Client
}

func (f *fakeEtcd) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (f *fakeEtcd) Client(context.Context) (*clientv3.Client, error) {
	return f.client, nil
}

// Test_quorum_convergence_after_scaleup tests that the dapr cron wrapper
// survives rapid quorum changes. The fix uses an events/loop to decouple the
// WatchLeadership channel read from host broadcast processing, preventing the
// go-etcd-cron wleaderCh send from blocking.
func Test_quorum_convergence_after_scaleup(t *testing.T) {
	t.Parallel()

	client := embeddedEtcdClient(t)
	etcdImpl := &fakeEtcd{client: client}

	crs := make([]Interface, 4)
	for i := range 4 {
		crs[i] = New(Options{
			ID:      strconv.Itoa(i),
			Host:    &schedulerv1pb.Host{Address: "127.0.0.1:" + strconv.Itoa(50000+i)},
			Etcd:    etcdImpl,
			Workers: 1,
		})
	}

	errCh := make(chan error, 4)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		for range 4 {
			select {
			case <-time.After(time.Second * 10):
				t.Fatal("timeout waiting for cron shutdown")
			case err := <-errCh:
				require.NoError(t, err)
			}
		}
	})

	for i := range 2 {
		go func() { errCh <- crs[i].Run(ctx) }()
	}

	for i := range 2 {
		_, err := crs[i].Client(ctx)
		require.NoError(t, err, "instance %d never became ready", i)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Get(t.Context(), "dapr/leadership", clientv3.WithPrefix())
		require.NoError(c, err)
		assert.Len(c, resp.Kvs, 2)
	}, time.Second*5, time.Millisecond*10)

	go func() { errCh <- crs[2].Run(ctx) }()

	_, err := crs[2].Client(ctx)
	require.NoError(t, err, "instance 2 never became ready")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var resp *clientv3.GetResponse
		resp, err = client.Get(t.Context(), "dapr/leadership", clientv3.WithPrefix())
		require.NoError(c, err)
		assert.Len(c, resp.Kvs, 3)
	}, time.Second*5, time.Millisecond*10)

	go func() { errCh <- crs[3].Run(ctx) }()

	_, err = crs[3].Client(ctx)
	require.NoError(t, err, "instance 3 never became ready")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Get(t.Context(), "dapr/leadership", clientv3.WithPrefix())
		require.NoError(c, err)
		assert.Len(c, resp.Kvs, 4)
	}, time.Second*10, time.Millisecond*10)

	for i := range 4 {
		_, err := crs[i].Client(ctx)
		require.NoError(t, err, "instance %d is no longer ready (cron likely exited silently)", i)
	}
}

func embeddedEtcdClient(t *testing.T) *clientv3.Client {
	t.Helper()

	cfg := embed.NewConfig()
	cfg.LogLevel = "error"
	cfg.Dir = t.TempDir()
	lurl, err := url.Parse("http://127.0.0.1:0")
	require.NoError(t, err)
	cfg.ListenPeerUrls = []url.URL{*lurl}
	cfg.ListenClientUrls = []url.URL{*lurl}

	etcd, err := embed.StartEtcd(cfg)
	require.NoError(t, err)
	t.Cleanup(etcd.Close)

	cl, err := clientv3.New(clientv3.Config{
		Endpoints: []string{etcd.Clients[0].Addr().String()},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cl.Close()) })

	select {
	case <-etcd.Server.ReadyNotify():
	case <-time.After(5 * time.Second):
		require.Fail(t, "etcd took too long to start")
	}

	return cl
}
