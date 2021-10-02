module app

go 1.17

require (
	github.com/dapr/dapr v1.3.1-0.20210916215627-82ef46fb541f
	go.opencensus.io v0.22.5
	google.golang.org/grpc v1.38.0
	google.golang.org/protobuf v1.26.0
)

require (
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.0.0-20210614182718-04defd469f4e // indirect
	golang.org/x/sys v0.0.0-20210616094352-59db8d763f22 // indirect
	golang.org/x/text v0.3.6 // indirect
	google.golang.org/genproto v0.0.0-20210524171403-669157292da3 // indirect
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
