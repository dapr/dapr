module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.24.13

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.78.0
	google.golang.org/grpc/examples v0.0.0-20250407062114-b368379ef8f6
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.opentelemetry.io/otel v1.40.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260128011058-8636f8732409 // indirect
)

replace github.com/dapr/dapr => ../../../
