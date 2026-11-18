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

package logs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
)

// countLines returns the number of captured log lines at the given level
// that contain every one of substrs.
func countLines(l *logline.LogLine, level string, substrs ...string) int {
	var n int
	for line := range bytes.SplitSeq(l.StdoutBuffer(), []byte("\n")) {
		if !bytes.Contains(line, []byte("level="+level+" ")) {
			continue
		}
		found := true
		for _, s := range substrs {
			if !bytes.Contains(line, []byte(s)) {
				found = false
				break
			}
		}
		if found {
			n++
		}
	}
	return n
}

// invoke calls method on the target app through the caller daprd and returns
// the response status code.
func invoke(t *testing.T, ctx context.Context, caller, target *daprd.Daprd, method string) int {
	t.Helper()
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", caller.HTTPPort(), target.AppID(), method)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode
}
