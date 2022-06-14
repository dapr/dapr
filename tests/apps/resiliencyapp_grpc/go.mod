module app

go 1.18

require (
	github.com/dapr/dapr v1.7.4
	github.com/golang/protobuf v1.5.2
	google.golang.org/grpc v1.47.0
	google.golang.org/grpc/examples v0.0.0-20220314230434-84793b56f63c
	google.golang.org/protobuf v1.28.0
)

require (
	golang.org/x/net v0.0.0-20220520000938-2e3eb7b945c2 // indirect
	golang.org/x/sys v0.0.0-20220412211240-33da011f77ad // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220107163113-42d7afdf6368 // indirect
)

replace github.com/dapr/dapr => ../../../
