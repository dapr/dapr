module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.19

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.50.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.28.1
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.0.0-20221004154528-8021a29435af // indirect
	golang.org/x/sys v0.0.0-20221010170243-090e33056c14 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20221010155953-15ba04fc1c0e // indirect
)

replace github.com/dapr/dapr => ../../../
