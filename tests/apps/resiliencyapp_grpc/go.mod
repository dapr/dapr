module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.23.1

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.67.0
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
	google.golang.org/protobuf v1.34.2
)

require (
	go.opentelemetry.io/otel v1.30.0 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240924160255-9d4c2d233b61 // indirect
)

replace github.com/dapr/dapr => ../../../
