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

package scheduler

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// FillQuota writes 1MiB values under the given etcd key prefix until etcd
// returns "database space exceeded". Requires the scheduler to have been
// started with a low --etcd-space-quota (see WithEtcdSpaceQuota).
func (s *Scheduler) FillQuota(t *testing.T, ctx context.Context, prefix string) {
	t.Helper()
	cli := s.ETCDClient(t, ctx)
	payload := strings.Repeat("x", 1024*1024)
	require.Eventually(t, func() bool {
		for i := range 64 {
			fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := cli.Put(fctx, prefix+strconv.Itoa(i), payload)
			cancel()
			if err != nil {
				return strings.Contains(err.Error(), "database space exceeded")
			}
		}
		return false
	}, 30*time.Second, 10*time.Millisecond)
}

// RecoverQuota deletes keys under prefix, compacts revisions, defragments
// the backend, and disarms any etcd alarms so writes can resume. Each etcd
// operation is bounded by its own 5s timeout so a hung backend cannot stall
// the test until the suite-level deadline.
func (s *Scheduler) RecoverQuota(t *testing.T, ctx context.Context, prefix string) {
	t.Helper()
	cli := s.ETCDClient(t, ctx)
	endpoint := "127.0.0.1:" + strconv.Itoa(s.EtcdClientPort())

	withTimeout := func(fn func(context.Context)) {
		octx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		fn(octx)
	}

	withTimeout(func(octx context.Context) {
		_, err := cli.Delete(octx, prefix, clientv3.WithPrefix())
		require.NoError(t, err)
	})

	var rev int64
	withTimeout(func(octx context.Context) {
		status, err := cli.Status(octx, endpoint)
		require.NoError(t, err)
		rev = status.Header.Revision
	})

	withTimeout(func(octx context.Context) {
		_, err := cli.Compact(octx, rev, clientv3.WithCompactPhysical())
		require.NoError(t, err)
	})

	withTimeout(func(octx context.Context) {
		_, err := cli.Defragment(octx, endpoint)
		require.NoError(t, err)
	})

	var alarms *clientv3.AlarmResponse
	withTimeout(func(octx context.Context) {
		var err error
		alarms, err = cli.AlarmList(octx)
		require.NoError(t, err)
	})
	for _, a := range alarms.Alarms {
		withTimeout(func(octx context.Context) {
			_, err := cli.AlarmDisarm(octx, (*clientv3.AlarmMember)(a))
			require.NoError(t, err)
		})
	}
}
