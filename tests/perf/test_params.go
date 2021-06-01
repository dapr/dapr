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

	qpsEnvVar                          = "DAPR_PERF_QPS"
	clientConnectionsEnvVar            = "DAPR_PERF_CONNECTIONS"
	testDurationEnvVar                 = "DAPR_TEST_DURATION"
	payloadSizeEnvVar                  = "DAPR_PAYLOAD_SIZE"
	payloadEnvVar                      = "DAPR_PAYLOAD"
	runCrossNetworkTests               = "DAPR_XNET_RUN"
	crossNetworkBaselineEndpointEnvVar = "DAPR_XNET_BASELINE_ENDPOINT"
	crossNetworkAppIDEnvVar            = "DAPR_XNET_APP_ID"
)

type TestParameters struct {
	QPS                          int    `json:"qps"`
	ClientConnections            int    `json:"clientConnections"`
	TargetEndpoint               string `json:"targetEndpoint"`
	TestDuration                 string `json:"testDuration"`
	PayloadSizeKB                int    `json:"payloadSizeKB"`
	Payload                      string `json:"payload"`
	StdClient                    bool   `json:"stdClient"`
	RunCrossNetworkTests         bool   `json:"runCrossNetworkTests"`
	CrossNetworkBaselineEndpoint string `json:"crossNetworkBaselineEndpoint"`
	CrossNetworkAppID            string `json:"crossNetworkAppID"`
}

func Params() TestParameters {
	return TestParameters{
		QPS: atoi(getEnvVarOrDefault(qpsEnvVar, ""), defaultQPS),
		ClientConnections: atoi(
			getEnvVarOrDefault(clientConnectionsEnvVar, ""), defaultClientConnections),
		TestDuration: getEnvVarOrDefault(testDurationEnvVar, defaultTestDuration),
		Payload:      getEnvVarOrDefault(payloadEnvVar, defaultPayload),
		PayloadSizeKB: atoi(
			getEnvVarOrDefault(payloadSizeEnvVar, ""), defaultPayloadSizeKB),
		RunCrossNetworkTests: isSet(
			getEnvVarOrDefault(runCrossNetworkTests, "")),
		CrossNetworkBaselineEndpoint: getEnvVarOrDefault(crossNetworkBaselineEndpointEnvVar, ""),
		CrossNetworkAppID:            getEnvVarOrDefault(crossNetworkAppIDEnvVar, ""),
	}
}

func atoi(str string, defaultValue int) int {
	val, err := strconv.Atoi(str)
	if err != nil {
		return defaultValue
	}

	return val
}

func isSet(val string) bool {
	return val != ""
}

func getEnvVarOrDefault(envVar, defaultValue string) string {
	if val, ok := os.LookupEnv(envVar); ok && val != "" {
		return val
	}

	return defaultValue
}
