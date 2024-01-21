module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.21

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.60.1
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
	google.golang.org/protobuf v1.32.0
)

require (
	github.com/golang/protobuf v1.5.3 // indirect
	go.opentelemetry.io/otel v1.21.0 // indirect
	go.opentelemetry.io/otel/trace v1.21.0 // indirect
	golang.org/x/net v0.20.0 // indirect
	golang.org/x/sys v0.16.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231212172506-995d672761c0 // indirect
)

replace github.com/dapr/dapr => ../../../
