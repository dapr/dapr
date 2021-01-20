module app

go 1.15

require (
	github.com/dapr/dapr v1.0.0-rc.2.0.20210120190900-dfe7edb55c6d
	github.com/golang/protobuf v1.4.3
	github.com/google/uuid v1.1.2
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.33.1
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
