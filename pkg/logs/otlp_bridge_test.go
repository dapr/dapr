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

package logs

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestOTLPOptionsFields(t *testing.T) {
	opts := OTLPOptions{
		Protocol:       "grpc",
		EndpointAddress: "localhost:4317",
		IsSecure:       true,
		Headers:        map[string]string{"X-Key": "value"},
		Timeout:        10 * time.Second,
	}
	assert.Equal(t, "grpc", opts.Protocol)
	assert.Equal(t, "localhost:4317", opts.EndpointAddress)
	assert.True(t, opts.IsSecure)
	assert.Equal(t, map[string]string{"X-Key": "value"}, opts.Headers)
	assert.Equal(t, 10*time.Second, opts.Timeout)
}

func TestBridgeImplementsLogrusHook(t *testing.T) {
	// Compile-time check that Bridge.Hook() returns a logrus.Hook
	var b *Bridge
	_ = func() logrus.Hook { return b.Hook() }
}

func TestDefaultTimeout(t *testing.T) {
	assert.Equal(t, 15*time.Second, defaultOTLPLogTimeout)
}
