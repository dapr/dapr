package perf

const (
	DefaultQPS               = 1
	DefaultClientConnections = 1
	DefaultTestDuration      = "1m"
	QPSEnvVar                = "DAPR_PERF_QPS"
	ClientConnectionsEnvVar  = "DAPR_PERF_CONNECTIONS"
	TestDurationEnvVar       = "DAPR_TEST_DURATION"
	PayloadSizeEnvVar        = "DAPR_PAYLOAD_SIZE"
)

type TestParameters struct {
	QPS               int    `json:"qps"`
	ClientConnections int    `json:"clientConnections"`
	TargetEndpoint    string `json:"targetEndpoint"`
	TestDuration      string `json:"testDuration"`
	PayloadSizeKB     int    `json:"payloadSizeKB"`
}

func ParamsFromDefaults() TestParameters {
	return TestParameters{
		QPS:               DefaultQPS,
		ClientConnections: DefaultClientConnections,
		TestDuration:      DefaultTestDuration,
	}
}
