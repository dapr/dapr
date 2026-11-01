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

package output

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(binarymetadata))
}

// binarymetadata asserts that binary gRPC metadata (keys ending in "-bin") is
// not forwarded to output binding component metadata, while text tracing and
// custom metadata still is. The bindings.metadataprobe component echoes the
// metadata it receives as JSON in the response data.
type binarymetadata struct {
	daprd *daprd.Daprd
}

func (b *binarymetadata) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t,
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: probe
spec:
  type: bindings.metadataprobe
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *binarymetadata) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	// Valid OpenCensus binary trace context, 29 bytes. The trace ID contains
	// a horizontal TAB byte (0x09), which is what breaks components that
	// sanitize or validate string metadata.
	traceBin := []byte{
		0x00,
		0x00, 0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x09, 0x47, 0x36,
		0x01, 0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7,
		0x02, 0x01,
	}

	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx = grpcMetadata.AppendToOutgoingContext(ctx,
		"grpc-trace-bin", string(traceBin),
		"foo-bin", string([]byte{0x00, 0x09, 0xff}),
		"traceparent", tp,
		"custom-key", "custom-value",
	)

	resp, err := client.InvokeBinding(ctx, &rtv1.InvokeBindingRequest{
		Name:      "probe",
		Operation: "get",
	})
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(resp.GetData(), &got))

	assert.NotContains(t, got, "grpc-trace-bin")
	assert.NotContains(t, got, "dapr-grpc-trace-bin")
	assert.NotContains(t, got, "foo-bin")
	assert.Equal(t, tp, got["traceparent"])
	assert.Equal(t, "custom-value", got["custom-key"])
}
