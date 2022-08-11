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

package apphealth

import (
	"time"
)

const (
	// DefaultProbeInterval is the default interval for app health probes.
	DefaultProbeInterval = 5 * time.Second
	// DefaultProbeTimeout is the default value for probe timeouts.
	DefaultProbeTimeout = 500 * time.Millisecond
	// DefaultThreshold is the default threshold for determining failures in app health checks.
	DefaultThreshold = int32(3)
)

// Config is the configuration object for the app health probes.
type Config struct {
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	ProbeOnly     bool
	Threshold     int32
}
