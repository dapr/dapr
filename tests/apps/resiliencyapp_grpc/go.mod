module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.25.8

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.79.3
	google.golang.org/grpc/examples v0.0.0-20250407062114-b368379ef8f6
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.opentelemetry.io/otel v1.40.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260319201613-d00831a3d3e7 // indirect
)

replace github.com/dapr/dapr => ../../../
