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

func Params(opts ...Opt) TestParameters {
	params := TestParameters{
		QPS: atoi(getEnvVarOrDefault(qpsEnvVar, ""), defaultQPS),
		ClientConnections: atoi(
			getEnvVarOrDefault(clientConnectionsEnvVar, ""), defaultClientConnections),
		TestDuration: getEnvVarOrDefault(testDurationEnvVar, defaultTestDuration),
		Payload:      getEnvVarOrDefault(payloadEnvVar, defaultPayload),
		PayloadSizeKB: atoi(
			getEnvVarOrDefault(payloadSizeEnvVar, ""), defaultPayloadSizeKB),
	}

	for _, o := range opts {
		o(&params)
	}
	return params
}

func atoi(str string, defaultValue int) int {
	val, err := strconv.Atoi(str)
	if err != nil {
		return defaultValue
	}

	return val
}

func getEnvVarOrDefault(envVar, defaultValue string) string {
	if val, ok := os.LookupEnv(envVar); ok && val != "" {
		return val
	}

	return defaultValue
}
