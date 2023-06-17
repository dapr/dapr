/*
Copyright 2023 The Dapr Authors
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

package protocol

import (
	"testing"
)

func TestProtocolIsHttp(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		want     bool
	}{
		{
			name:     "http",
			protocol: HTTPProtocol,
			want:     true,
		},
		{
			name:     "https",
			protocol: HTTPSProtocol,
			want:     true,
		},
		{
			name:     "h2c",
			protocol: H2CProtocol,
			want:     true,
		},
		{
			name:     "grpc",
			protocol: GRPCProtocol,
			want:     false,
		},
		{
			name:     "grpcs",
			protocol: GRPCSProtocol,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.protocol.IsHTTP(); got != tt.want {
				t.Errorf("Protocol.IsHTTP() = %v, want %v", got, tt.want)
			}
		})
	}
}
