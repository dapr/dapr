module app

go 1.17

require (
	github.com/dapr/dapr v0.11.3
	github.com/golang/protobuf v1.4.3
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.34.0
)

require (
	github.com/google/go-cmp v0.5.4 // indirect
	golang.org/x/net v0.0.0-20201209123823-ac852fbbde11 // indirect
	golang.org/x/sys v0.0.0-20210104204734-6f8348627aad // indirect
	golang.org/x/text v0.3.4 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/genproto v0.0.0-20201214200347-8c77b98c765d // indirect
	google.golang.org/protobuf v1.25.0 // indirect
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
