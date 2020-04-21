package perf

const (
	DefaultQPS               = 1
	DefaultClientConnections = 1
	DefaultTestDuration      = "1m"
	QPSEnvVar                = "DAPR_PERF_QPS"
	ClientConnectionsEnvVar  = "DAPR_PERF_CONNECTIONS"
	TestDurationEnvVar       = "DAPR_TEST_DURATION"
)

type TestParameters struct {
	QPS               int    `json:"qps"`
	ClientConnections int    `json:"clientConnections"`
	TargetEndpoint    string `json:"targetEndpoint"`
	TestDuration      string `json:"testDuration"`
}

func ParamsFromDefaults() TestParameters {
	return TestParameters{
		QPS:               DefaultQPS,
		ClientConnections: DefaultClientConnections,
		TestDuration:      DefaultTestDuration,
	}
}
