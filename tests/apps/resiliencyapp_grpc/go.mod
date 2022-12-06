module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.19

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.51.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.28.1
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.1.0 // indirect
	golang.org/x/sys v0.1.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	google.golang.org/genproto v0.0.0-20221027153422-115e99e71e1c // indirect
)

replace github.com/dapr/dapr => ../../../
