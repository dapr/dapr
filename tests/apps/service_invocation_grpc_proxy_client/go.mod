module github.com/dapr/dapr/tests/apps/service_invocation_grpc_proxy_client

go 1.23.1

require (
	github.com/dapr/dapr v0.0.0-00010101000000-000000000000
	github.com/gorilla/mux v1.8.1
	google.golang.org/grpc v1.67.0
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20
)

require (
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/otel v1.30.0 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240924160255-9d4c2d233b61 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

replace github.com/dapr/dapr => ../../../
