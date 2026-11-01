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

package sighup

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sizeBuckets))
}

// sizeBuckets ensures that the runtime restarted by SIGHUP exports the
// request size histogram with the default size buckets, and that the
// histogram counts only the samples recorded by the replacement runtime. The
// replacement registers its views on a new meter, which must get its own
// size distribution rather than the one the previous meter registered.
type sizeBuckets struct {
	daprd *daprd.Daprd
	log   *log.Log
}

func (s *sizeBuckets) Setup(t *testing.T) []framework.Option {
	s.log = log.New()

	s.daprd = daprd.New(t,
		daprd.WithAppID("testapp"),
		daprd.WithInMemoryStateStore("mystore"),
		daprd.WithExecOptions(exec.WithStdout(s.log), exec.WithStderr(s.log)),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *sizeBuckets) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	const prefix = "dapr_http_server_request_bytes_bucket|app_id:testapp|le:"

	// Every bound of the default size distribution, in bytes.
	bounds := []uint64{
		1 << 10, 2 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10,
		1 << 20, 4 << 20, 16 << 20, 64 << 20, 256 << 20, 1 << 30, 4 << 30,
	}

	// A body of a little over 3 KiB lands in the (2 KiB, 4 KiB] bucket.
	// Health checks also record into this histogram, with an empty body, so
	// the assertions below only look at that one bucket rather than at the
	// total count.
	body := `[{"key":"k","value":"` + strings.Repeat("x", 3<<10) + `"}]`
	save := func() {
		s.daprd.HTTPPost(t, ctx, "/v1.0/state/mystore", strings.NewReader(body), http.StatusNoContent)
	}
	inBucket := func(c *assert.CollectT) int {
		m := s.daprd.Metrics(c, ctx).All()
		for _, b := range bounds {
			_, ok := m[fmt.Sprintf("%s%d", prefix, b)]
			assert.True(c, ok, "missing bucket le=%d", b)
		}
		return int(m[fmt.Sprintf("%s%d", prefix, 4<<10)] - m[fmt.Sprintf("%s%d", prefix, 2<<10)])
	}

	save()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, inBucket(c))
	}, 10*time.Second, 10*time.Millisecond)

	s.log.Reset()
	s.daprd.SignalHUP(t)

	// Wait for the replacement to report itself running: requests sent any
	// earlier are served, and recorded, by the runtime on its way out.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, s.log.Contains("dapr initialized. Status: Running"))
	}, 10*time.Second, 10*time.Millisecond)
	s.daprd.WaitUntilRunning(t, ctx)

	save()
	save()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 2, inBucket(c))
	}, 10*time.Second, 10*time.Millisecond)
}
