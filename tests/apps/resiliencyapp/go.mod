module github.com/dapr/dapr/tests/apps/resiliencyapp

go 1.20

require (
	github.com/dapr/dapr v1.7.4
	github.com/gorilla/mux v1.8.0
	google.golang.org/grpc v1.53.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.30.0
)

require (
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/uuid v1.3.0 // indirect
	golang.org/x/net v0.8.0 // indirect
	golang.org/x/sys v0.6.0 // indirect
	golang.org/x/text v0.8.0 // indirect
	google.golang.org/genproto v0.0.0-20230306155012-7f2fa6fef1f4 // indirect
)

replace github.com/dapr/dapr => ../../../
