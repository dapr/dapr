/*
Copyright 2025 The Dapr Authors
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

package etcd

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Etcd struct {
	fp        []*ports.Ports
	configs   []*embed.Config
	etcds     []*embed.Etcd
	endpoints []string
	username  *string
	password  *string
}

func New(t *testing.T, fopts ...Option) *Etcd {
	t.Helper()

	opts := options{
		nodes: 1,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.Equal(t, opts.username != nil, opts.password != nil, "username and password must be set together")
	require.Positive(t, opts.nodes, "nodes must be greater than 0")

	configs := make([]*embed.Config, opts.nodes)
	endpoints := make([]string, opts.nodes)
	clusterEntries := make([]string, opts.nodes)
	fp := make([]*ports.Ports, opts.nodes)

	for i := range opts.nodes {
		config := embed.NewConfig()
		config.LogLevel = "error"
		config.Dir = t.TempDir()

		fp[i] = ports.Reserve(t, 2)

		clientEndpoint := "http://127.0.0.1:" + strconv.Itoa(fp[i].Port(t))
		lurl, err := url.Parse(clientEndpoint)
		require.NoError(t, err)
		config.ListenClientUrls = []url.URL{*lurl}
		config.AdvertiseClientUrls = []url.URL{*lurl}

		peerEndpoint := "http://127.0.0.1:" + strconv.Itoa(fp[i].Port(t))
		lurl, err = url.Parse(peerEndpoint)
		require.NoError(t, err)
		config.ListenPeerUrls = []url.URL{*lurl}
		config.AdvertisePeerUrls = []url.URL{*lurl}

		config.ListenMetricsUrls = nil

		config.Name = "etcd" + strconv.Itoa(i)

		configs[i] = config
		endpoints[i] = clientEndpoint
		clusterEntries[i] = config.Name + "=" + peerEndpoint
	}

	initialCluster := strings.Join(clusterEntries, ",")
	for _, config := range configs {
		config.InitialCluster = initialCluster
	}

	return &Etcd{
		fp:        fp,
		configs:   configs,
		endpoints: endpoints,
	}
}

func (e *Etcd) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	for i, config := range e.configs {
		e.fp[i].Free(t)

		etcd, err := embed.StartEtcd(config)
		require.NoError(t, err)

		e.etcds = append(e.etcds, etcd)
	}

	for _, etcd := range e.etcds {
		select {
		case <-etcd.Server.ReadyNotify():
		case <-ctx.Done():
			assert.Fail(t, "server took too long to start")
		}
	}

	e.setupUserPass(t, ctx)
}

func (e *Etcd) Cleanup(t *testing.T) {
	t.Helper()

	for i, et := range e.etcds {
		et.Close()
		e.fp[i].Cleanup(t)
	}
}

func (e *Etcd) Endpoints() []string {
	return e.endpoints
}

func (e *Etcd) setupUserPass(t *testing.T, ctx context.Context) {
	t.Helper()

	if e.username == nil {
		return
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   e.endpoints,
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	_, err = client.Auth.UserAdd(ctx, *e.username, *e.password)
	require.NoError(t, err)

	_, err = client.Auth.RoleAdd(ctx, "root")
	require.NoError(t, err)

	_, err = client.Auth.UserGrantRole(ctx, *e.username, "root")
	require.NoError(t, err)

	_, err = client.Auth.RoleGrantPermission(ctx, "root", "", "", clientv3.PermissionType(clientv3.PermReadWrite))
	require.NoError(t, err)

	_, err = client.AuthEnable(ctx)
	require.NoError(t, err)
}
