module app

go 1.14

require (
	github.com/dapr/dapr v0.9.1-0.20200812210155-e3f5ee0b162d
	github.com/golang/protobuf v1.3.3
	go.opencensus.io v0.22.3
	google.golang.org/grpc v1.26.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
