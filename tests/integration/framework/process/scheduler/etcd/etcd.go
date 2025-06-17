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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Etcd struct {
	fp        *ports.Ports
	config    *embed.Config
	etcd      *embed.Etcd
	endpoints []string
}

func New(t *testing.T, fopts ...Option) *Etcd {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	fp := ports.Reserve(t, 2)

	config := embed.NewConfig()
	config.LogLevel = "info"
	config.Dir = t.TempDir()

	clientEndpoint := "http://127.0.0.1:" + strconv.Itoa(fp.Port(t))
	lurl, err := url.Parse(clientEndpoint)
	require.NoError(t, err)
	config.ListenClientUrls = []url.URL{*lurl}

	lurl, err = url.Parse("http://127.0.0.1:" + strconv.Itoa(fp.Port(t)))
	require.NoError(t, err)
	config.ListenPeerUrls = []url.URL{*lurl}

	return &Etcd{
		fp:        fp,
		config:    config,
		endpoints: []string{clientEndpoint},
	}
}

func (e *Etcd) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	e.fp.Free(t)
	etcd, err := embed.StartEtcd(e.config)
	require.NoError(t, err)

	select {
	case <-etcd.Server.ReadyNotify():
	case <-ctx.Done():
		assert.Fail(t, "server took too long to start")
	}

	e.etcd = etcd
}

func (e *Etcd) Cleanup(t *testing.T) {
	t.Helper()

	if e.etcd != nil {
		e.etcd.Close()
	}
	e.fp.Cleanup(t)
}

func (e *Etcd) Endpoints() []string {
	return e.endpoints
}
