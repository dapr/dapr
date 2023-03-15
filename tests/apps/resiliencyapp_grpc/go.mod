module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.20

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.53.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.28.1
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.7.0 // indirect
	golang.org/x/sys v0.5.0 // indirect
	golang.org/x/text v0.7.0 // indirect
	google.golang.org/genproto v0.0.0-20230223222841-637eb2293923 // indirect
)

replace github.com/dapr/dapr => ../../../
