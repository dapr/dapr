module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.24.2

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.71.1
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
	google.golang.org/protobuf v1.36.6
)

require (
	go.opentelemetry.io/otel v1.35.0 // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250409194420-de1ac958c67a // indirect
)

replace github.com/dapr/dapr => ../../../
