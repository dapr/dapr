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

// Protocol is a communications protocol.
type Protocol string

const (
	// GRPCProtocol is the gRPC communication protocol.
	GRPCProtocol Protocol = "grpc"
	// GRPCSProtocol is the gRPC communication protocol with TLS (without validating certificates).
	GRPCSProtocol Protocol = "grpcs"
	// HTTPProtocol is the HTTP communication protocol.
	HTTPProtocol Protocol = "http"
	// HTTPSProtocol is the HTTPS communication protocol with TLS (without validating certificates).
	HTTPSProtocol Protocol = "https"
	// H2CProtocol is the HTTP/2 Cleartext communication protocol (HTTP/2 without TLS).
	H2CProtocol Protocol = "h2c"
)

// IsHTTP returns true if the app protocol is using HTTP (including HTTPS and H2C).
func (p Protocol) IsHTTP() bool {
	switch p {
	case HTTPProtocol, HTTPSProtocol, H2CProtocol:
		return true
	default:
		return false
	}
}

// HasTLS returns true if the app protocol is using TLS.
func (p Protocol) HasTLS() bool {
	switch p {
	case HTTPSProtocol, GRPCSProtocol:
		return true
	default:
		return false
	}
}
