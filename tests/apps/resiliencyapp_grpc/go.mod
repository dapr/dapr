module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.22.4

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.64.0
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
	google.golang.org/protobuf v1.34.2
)

require (
	go.opentelemetry.io/otel v1.27.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240624140628-dc46fd24d27d // indirect
)

replace github.com/dapr/dapr => ../../../
