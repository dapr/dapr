module app

go 1.14

require (
	github.com/dapr/dapr v0.9.1-0.20200811173032-6c5899e12804
	github.com/gorilla/mux v1.7.3
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36

replace github.com/dapr/dapr => github.com/youngbupark/dapr v0.7.1-0.20200811203747-141d9ffe184c
