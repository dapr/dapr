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
	"os"
	"strconv"
)

const (
	defaultQPS               = 1
	defaultClientConnections = 1
	defaultPayloadSizeKB     = 0
	defaultPayload           = ""
	defaultTestDuration      = "1m"

	qpsEnvVar               = "DAPR_PERF_QPS"
	clientConnectionsEnvVar = "DAPR_PERF_CONNECTIONS"
	testDurationEnvVar      = "DAPR_TEST_DURATION"
	payloadSizeEnvVar       = "DAPR_PAYLOAD_SIZE"
	payloadEnvVar           = "DAPR_PAYLOAD"
)

type TestParameters struct {
	QPS               int    `json:"qps"`
	ClientConnections int    `json:"clientConnections"`
	TargetEndpoint    string `json:"targetEndpoint"`
	TestDuration      string `json:"testDuration"`
	PayloadSizeKB     int    `json:"payloadSizeKB"`
	Payload           string `json:"payload"`
	StdClient         bool   `json:"stdClient"`
	Grpc              bool   `json:"grpc"`
	Dapr              string `json:"dapr"`
}
type Opt = func(*TestParameters)

// WithQPS sets the test taget query per second.
func WithQPS(qps int) Opt {
	return func(tp *TestParameters) {
		tp.QPS = qps
	}
}

// WithConnections set the total client connections.
func WithConnections(connections int) Opt {
	return func(tp *TestParameters) {
		tp.ClientConnections = connections
	}
}

// WithDuration sets the total test duration.
// accepts the time notation `1m`, `10s`, etc.
func WithDuration(duration string) Opt {
	return func(tp *TestParameters) {
		tp.TestDuration = duration
	}
}

// WithPayloadSize sets the test random payload size in KB.
func WithPayloadSize(sizeKB int) Opt {
	return func(tp *TestParameters) {
		tp.PayloadSizeKB = sizeKB
	}
}

// WithPayload sets the test payload.
func WithPayload(payload string) Opt {
	return func(tp *TestParameters) {
		tp.Payload = payload
	}
}

func noop(tp *TestParameters) {}

func Params(opts ...Opt) TestParameters {
	params := TestParameters{
		QPS:               defaultQPS,
		ClientConnections: defaultClientConnections,
		TestDuration:      defaultTestDuration,
		Payload:           defaultPayload,
		PayloadSizeKB:     defaultPayloadSizeKB,
	}

	for _, o := range append(opts, // set environment variables to have precedence over manually-set and default params.
		useEnvVar(qpsEnvVar, toStrParam(WithQPS)),
		useEnvVar(clientConnectionsEnvVar, toStrParam(WithConnections)),
		useEnvVar(testDurationEnvVar, WithDuration),
		useEnvVar(payloadEnvVar, WithPayload),
		useEnvVar(payloadSizeEnvVar, toStrParam(WithPayloadSize)),
	) {
		o(&params)
	}
	return params
}

// toStrParam receives a function A that receives a int as a parameter and returns a function B
// that accepts a string. Applies the Atoi conversion on the given string from function B and if it fails it returns a no-op operation, otherwise the value
// is applied to the received function.
func toStrParam(opt func(value int) Opt) func(value string) Opt {
	return func(value string) Opt {
		val, err := strconv.Atoi(value)
		if err != nil {
			return noop
		}
		return opt(val)
	}
}

// useEnvVar if the provided envVar exists it will be applied as a parameter of the `opt` function and returned as a Opt,
// otherwise it will return a no-op function.
func useEnvVar(envVar string, opt func(value string) Opt) Opt {
	if val, ok := os.LookupEnv(envVar); ok && val != "" {
		return opt(val)
	}

	return noop
}
