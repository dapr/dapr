module github.com/dapr/dapr/tests/apps/resiliencyapp

go 1.19

require (
	github.com/dapr/dapr v1.7.4
	github.com/gorilla/mux v1.8.0
	google.golang.org/grpc v1.52.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.28.1
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	golang.org/x/net v0.5.0 // indirect
	golang.org/x/sys v0.4.0 // indirect
	golang.org/x/text v0.6.0 // indirect
	google.golang.org/genproto v0.0.0-20230123190316-2c411cf9d197 // indirect
)

replace github.com/dapr/dapr => ../../../
