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

package config

import (
	"time"

	"github.com/dapr/dapr/pkg/config/protocol"
)

const (
	// AppHealthConfigDefaultProbeInterval is the default interval for app health probes.
	AppHealthConfigDefaultProbeInterval = 5 * time.Second
	// AppHealthConfigDefaultProbeTimeout is the default value for probe timeouts.
	AppHealthConfigDefaultProbeTimeout = 500 * time.Millisecond
	// AppHealthConfigDefaultThreshold is the default threshold for determining failures in app health checks.
	AppHealthConfigDefaultThreshold = int32(3)
)

// AppHealthConfig is the configuration object for the app health probes.
type AppHealthConfig struct {
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	ProbeOnly     bool
	Threshold     int32
}

// AppConnectionConfig holds the configuration for the app connection.
type AppConnectionConfig struct {
	ChannelAddress      string
	HealthCheck         *AppHealthConfig
	HealthCheckHTTPPath string
	MaxConcurrency      int
	Port                int
	Protocol            protocol.Protocol
}
