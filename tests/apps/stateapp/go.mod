module app

go 1.15

require (
	github.com/dapr/dapr v0.11.1-0.20201106213210-a19c1322d69a
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.33.1
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
