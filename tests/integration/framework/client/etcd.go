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

package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdClient struct {
	client *clientv3.Client
}

// Returns an adapted Etcd client for tests.
func Etcd(t *testing.T, cfg clientv3.Config) *EtcdClient {
	etcdClient, err := clientv3.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, etcdClient.Close()) })

	return &EtcdClient{etcdClient}
}

func (c *EtcdClient) ListAllKeys(ctx context.Context, prefix string) ([]string, error) {
	r := []string{}

	// Start key for pagination
	startKey := prefix

	// Loop until all keys are retrieved
	for {
		resp, err := c.client.Get(ctx, startKey, clientv3.WithPrefix(), clientv3.WithLimit(1000))
		if err != nil {
			return nil, err
		}

		// Process keys
		for _, kv := range resp.Kvs {
			r = append(r, string(kv.Key))
		}

		// If there are more keys, set the start key to the last key + 1
		if resp.More {
			lastKey := resp.Kvs[len(resp.Kvs)-1].Key
			startKey = string(lastKey) + "\x00"
		} else {
			// No more keys
			break
		}
	}

	return r, nil
}

func (c *EtcdClient) Get(t *testing.T, ctx context.Context, prefix string, opts ...clientv3.OpOption) []string {
	t.Helper()

	opts = append([]clientv3.OpOption{clientv3.WithPrefix()}, opts...)

	resp, err := c.client.Get(ctx, prefix, opts...)
	require.NoError(t, err)

	keys := make([]string, len(resp.Kvs))
	for i, kv := range resp.Kvs {
		keys[i] = string(kv.Key)
	}

	return keys
}
