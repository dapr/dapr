module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.24.4

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.72.1
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
	google.golang.org/protobuf v1.36.6
)

require (
	go.opentelemetry.io/otel v1.35.0 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250512202823-5a2f75b736a9 // indirect
)

replace github.com/dapr/dapr => ../../../
