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

package metadata

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bodyread))
}

// bodyread tests that a failure to read the PUT /v1.0/metadata/{key} request
// body returns the standardized error: HTTP 400, the legacy ERR_BODY_READ
// error code, and a google.rpc.ErrorInfo detail with reason
// DAPR_METADATA_BODY_READ.
type bodyread struct {
	daprd *procdaprd.Daprd
}

func (b *bodyread) Setup(t *testing.T) []framework.Option {
	b.daprd = procdaprd.New(t)

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *bodyread) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", b.daprd.HTTPPort()))
	require.NoError(t, err)
	defer conn.Close()

	tcpConn, ok := conn.(*net.TCPConn)
	require.True(t, ok)

	// Declare a longer body than is sent, then close only the write side.
	// Reading the body fails with an unexpected EOF, while the connection can
	// still carry the response back.
	_, err = tcpConn.Write([]byte("PUT /v1.0/metadata/bodyread HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 100\r\n" +
		"\r\n" +
		"short"))
	require.NoError(t, err)
	require.NoError(t, tcpConn.CloseWrite())

	require.NoError(t, tcpConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(tcpConn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	// The status code and the legacy error code were returned before the
	// Metadata API error standardization too; the ErrorInfo reason is new.
	assert.Equal(t, "ERR_BODY_READ", body["errorCode"])

	details, ok := body["details"].([]any)
	require.True(t, ok, "expected error details in response: %v", body)

	var reasons []any
	for _, d := range details {
		if m, isMap := d.(map[string]any); isMap && m["@type"] == "type.googleapis.com/google.rpc.ErrorInfo" {
			reasons = append(reasons, m["reason"])
		}
	}
	assert.Equal(t, []any{"DAPR_METADATA_BODY_READ"}, reasons)
}
