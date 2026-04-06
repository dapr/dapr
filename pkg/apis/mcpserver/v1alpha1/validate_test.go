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

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateResource_ExactlyOneTransport(t *testing.T) {
	t.Run("valid: streamableHTTP only", func(t *testing.T) {
		s := &MCPServer{}
		s.Name = "test"
		s.Spec.Endpoint.StreamableHTTP = &MCPStreamableHTTP{URL: "http://example.com"}
		err := ValidateResource(s)
		require.NoError(t, err)
	})

	t.Run("valid: sse only", func(t *testing.T) {
		s := &MCPServer{}
		s.Name = "test"
		s.Spec.Endpoint.SSE = &MCPSSE{URL: "http://example.com"}
		err := ValidateResource(s)
		require.NoError(t, err)
	})

	t.Run("valid: stdio only", func(t *testing.T) {
		s := &MCPServer{}
		s.Name = "test"
		s.Spec.Endpoint.Stdio = &MCPStdio{Command: "echo"}
		err := ValidateResource(s)
		require.NoError(t, err)
	})

	t.Run("invalid: no transport set", func(t *testing.T) {
		s := &MCPServer{}
		s.Name = "test"
		err := ValidateResource(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one of streamableHTTP, sse, or stdio must be set")
	})

	t.Run("invalid: two transports set", func(t *testing.T) {
		s := &MCPServer{}
		s.Name = "test"
		s.Spec.Endpoint.StreamableHTTP = &MCPStreamableHTTP{URL: "http://example.com"}
		s.Spec.Endpoint.SSE = &MCPSSE{URL: "http://example.com"}
		err := ValidateResource(s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one of streamableHTTP, sse, or stdio must be set")
	})
}
