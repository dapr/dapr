/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOTLPOptionsDefaults(t *testing.T) {
	opts := &OTLPOptions{
		Protocol:       "grpc",
		EndpointAddress: "localhost:4317",
		IsSecure:       true,
	}
	assert.Equal(t, "grpc", opts.Protocol)
	assert.Equal(t, "localhost:4317", opts.EndpointAddress)
	assert.True(t, opts.IsSecure)
}

func TestOTLPBridgeDefaults(t *testing.T) {
	assert.Equal(t, 30*time.Second, defaultOTLPExportInterval)
	assert.Equal(t, 15*time.Second, defaultOTLPTimeout)
}

func TestExporterOTLPBridgeFromOptions(t *testing.T) {
	e := &exporter{
		appID:    "test-app",
		otlpOpts: nil, // no OTLP config
	}
	// With nil otlpOpts, no bridge should be created
	assert.Nil(t, e.otlpBridge)
}

func TestExporterNoBridgeWhenDisabled(t *testing.T) {
	e := &exporter{
		enabled: false,
	}
	// Disabled exporter should not create a bridge
	assert.Nil(t, e.otlpBridge)
}
