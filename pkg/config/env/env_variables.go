package config

const (
	// HostAddress is the address of the instance.
	HostAddress string = "HOST_ADDRESS"
	// DaprGRPCPort is the dapr api grpc port.
	DaprGRPCPort string = "DAPR_GRPC_PORT"
	// DaprHTTPPort is the dapr api http port.
	DaprHTTPPort string = "DAPR_HTTP_PORT"
	// DaprMetricsPort is the dapr metrics port.
	DaprMetricsPort string = "DAPR_METRICS_PORT"
	// DaprProfilePort is the dapr performance profiling port.
	DaprProfilePort string = "DAPR_PROFILE_PORT"
	// DaprPort is the dapr internal grpc port (sidecar to sidecar).
	DaprPort string = "DAPR_PORT"
	// AppPort is the port of the application, http/grpc depending on mode.
	AppPort string = "APP_PORT"
	// AppID is the ID of the application.
	AppID string = "APP_ID"
	// OpenTelemetry target URL for OTLP exporter
	OtlpExporterEndpoint string = "OTEL_EXPORTER_OTLP_ENDPOINT"
	// OpenTelemetry target URL for OTLP exporter for traces
	OtlpExporterTracesEndpoint string = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	// OpenTelemetry disables client transport security
	OtlpExporterInsecure string = "OTEL_EXPORTER_OTLP_INSECURE"
	// OpenTelemetry transport protocol (grpc, http/protobuf, http/json)
	OtlpExporterProtocol string = "OTEL_EXPORTER_OTLP_PROTOCOL"
	// OpenTelemetry transport protocol for traces (grpc, http/protobuf, http/json)
	OtlpExporterTracesProtocol string = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	// OpenTelemetry headers to add to the request
	OtlpExporterHeaders string = "OTEL_EXPORTER_OTLP_HEADERS"
	// OpenTelemetry headers to add to the traces request
	OtlpExporterTracesHeaders string = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
	// OpenTelemetry timeout for the request
	OtlpExporterTimeout string = "OTEL_EXPORTER_OTLP_TIMEOUT"
	// OpenTelemetry timeout for the traces request
	OtlpExporterTracesTimeout string = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
)
