/*
Copyright 2022 The Dapr Authors
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
package perf

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParamsOpts(t *testing.T) {
	t.Run("default params should be used when env vars and params are absent", func(t *testing.T) {
		p := Params()

		assert.Equal(t, defaultClientConnections, p.ClientConnections)
		assert.Equal(t, defaultPayload, p.Payload)
		assert.Equal(t, defaultPayloadSizeKB, p.PayloadSizeKB)
		assert.Equal(t, defaultQPS, p.QPS)
		assert.Equal(t, defaultTestDuration, p.TestDuration)
	})
	t.Run("manually-set params should be used when specified", func(t *testing.T) {
		clientConnections := defaultClientConnections + 1
		payload := defaultPayload + "a"
		payloadSizeKB := defaultPayloadSizeKB + 1
		qps := defaultQPS + 1
		duration := defaultTestDuration + "a"
		p := Params(
			WithConnections(clientConnections),
			WithPayload(payload),
			WithPayloadSize(payloadSizeKB),
			WithQPS(qps),
			WithDuration(duration),
		)

		assert.Equal(t, p.ClientConnections, clientConnections)
		assert.Equal(t, p.Payload, payload)
		assert.Equal(t, p.PayloadSizeKB, payloadSizeKB)
		assert.Equal(t, p.QPS, qps)
		assert.Equal(t, p.TestDuration, duration)
	})
	t.Run("environment variables should override manually set params", func(t *testing.T) {
		clientConnections := defaultClientConnections + 1
		t.Setenv(clientConnectionsEnvVar, strconv.Itoa(clientConnections))
		payload := defaultPayload + "a"
		t.Setenv(payloadEnvVar, payload)
		payloadSizeKB := defaultPayloadSizeKB + 1
		t.Setenv(payloadSizeEnvVar, strconv.Itoa(payloadSizeKB))
		qps := defaultQPS + 1
		t.Setenv(qpsEnvVar, strconv.Itoa(qps))
		duration := defaultTestDuration + "a"
		t.Setenv(testDurationEnvVar, duration)

		p := Params(
			WithConnections(clientConnections+1),
			WithPayload(payload+"b"),
			WithPayloadSize(payloadSizeKB+1),
			WithQPS(qps+1),
			WithDuration(duration+"a"),
		)

		assert.Equal(t, p.ClientConnections, clientConnections)
		assert.Equal(t, p.Payload, payload)
		assert.Equal(t, p.PayloadSizeKB, payloadSizeKB)
		assert.Equal(t, p.QPS, qps)
		assert.Equal(t, p.TestDuration, duration)
	})
}
