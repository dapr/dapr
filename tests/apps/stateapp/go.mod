module app

go 1.14

require github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36 // indirect

require k8s.io/client-go v0.17.1 //indirect

require github.com/dapr/dapr v0.7.1

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
