module github.com/dapr/dapr/tests/apps/service_invocation_grpc_proxy_client

go 1.19

require (
	github.com/dapr/dapr v0.0.0-00010101000000-000000000000
	github.com/gorilla/mux v1.8.0
	google.golang.org/grpc v1.50.1
	google.golang.org/grpc/examples v0.0.0-20210610163306-6351a55c3895
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	golang.org/x/net v0.1.0 // indirect
	golang.org/x/sys v0.1.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	google.golang.org/genproto v0.0.0-20221018160656-63c7b68cfc55 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)

replace github.com/dapr/dapr => ../../../
