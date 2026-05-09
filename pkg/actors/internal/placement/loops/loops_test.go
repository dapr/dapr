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

package loops

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsActorLocal(t *testing.T) {
	tests := []struct {
		name   string
		target string
		host   string
		port   string
		want   bool
	}{
		// IPv4 exact match
		{"IPv4 match", "10.0.0.1:5000", "10.0.0.1", "5000", true},
		{"IPv4 no match different host", "10.0.0.2:5000", "10.0.0.1", "5000", false},
		{"IPv4 no match different port", "10.0.0.1:6000", "10.0.0.1", "5000", false},

		// IPv6 exact match
		{"IPv6 match", "[2001:db8::1]:5000", "2001:db8::1", "5000", true},
		{"IPv6 no match", "[2001:db8::2]:5000", "2001:db8::1", "5000", false},

		// Localhost equivalences
		{"localhost to 127.0.0.1", "127.0.0.1:5000", "localhost", "5000", true},
		{"127.0.0.1 to localhost", "localhost:5000", "127.0.0.1", "5000", true},
		{"localhost to ::1", "[::1]:5000", "localhost", "5000", true},
		{"::1 to localhost", "localhost:5000", "::1", "5000", true},

		// Non-localhost host with localhost target — should not match
		{"non-localhost host", "localhost:5000", "10.0.0.1", "5000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsActorLocal(tt.target, tt.host, tt.port))
		})
	}
}
