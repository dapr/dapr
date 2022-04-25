/*
Copyright 2021 The Dapr Authors
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

package http

// ServerConfig holds config values for an HTTP server.
type ServerConfig struct {
	AllowedOrigins     string
	AppID              string
	HostAddress        string
	Port               int
	APIListenAddresses []string
	PublicPort         *int
	ProfilePort        int
	EnableProfiling    bool
	MaxRequestBodySize int
	UnixDomainSocket   string
	ReadBufferSize     int
	StreamRequestBody  bool
	EnableAPILogging   bool
}

// NewServerConfig returns a new HTTP server config.
func NewServerConfig(appID string, hostAddress string, port int, apiListenAddresses []string, publicPort *int, profilePort int, allowedOrigins string, enableProfiling bool, maxRequestBodySize int, unixDomainSocket string, readBufferSize int, streamRequestBody bool, enableAPILogging bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:     allowedOrigins,
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		APIListenAddresses: apiListenAddresses,
		PublicPort:         publicPort,
		ProfilePort:        profilePort,
		EnableProfiling:    enableProfiling,
		MaxRequestBodySize: maxRequestBodySize,
		UnixDomainSocket:   unixDomainSocket,
		ReadBufferSize:     readBufferSize,
		StreamRequestBody:  streamRequestBody,
		EnableAPILogging:   enableAPILogging,
	}
}
