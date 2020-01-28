module github.com/dapr/dapr

go 1.13

require (
	github.com/AdhityaRamadhanus/fasthttpcors v0.0.0-20170121111917-d4c07198763a
	github.com/DataDog/zstd v1.4.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/dapr/components-contrib v0.0.0-20200117225401-373449171834
	github.com/docker/docker v1.13.1 // indirect
	github.com/eapache/go-resiliency v1.2.0 // indirect
	github.com/emicklei/go-restful v2.10.0+incompatible // indirect
	github.com/frankban/quicktest v1.5.0 // indirect
	github.com/fsnotify/fsnotify v1.4.7
	github.com/ghodss/yaml v1.0.0
	github.com/go-ozzo/ozzo-routing v2.1.4+incompatible // indirect
	github.com/gogo/protobuf v1.3.0 // indirect
	github.com/golang/gddo v0.0.0-20190815223733-287de01127ef // indirect
	github.com/golang/protobuf v1.3.2
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/gorilla/mux v1.7.1
	github.com/grandcat/zeroconf v0.0.0-20190424104450-85eadb44205c
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0
	github.com/grpc-ecosystem/grpc-gateway v1.11.2 // indirect
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.7 // indirect
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/json-iterator/go v1.1.8
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/konsorten/go-windows-terminal-sequences v1.0.2 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/mitchellh/mapstructure v1.1.2
	github.com/phayes/freeport v0.0.0-20171002181615-b8543db493a5
	github.com/pierrec/lz4 v2.3.0+incompatible // indirect
	github.com/qiangxue/fasthttp-routing v0.0.0-20160225050629-6ccdc2a18d87
	github.com/rcrowley/go-metrics v0.0.0-20190826022208-cac0b30c2563 // indirect
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/valyala/fasthttp v1.6.0
	go.opencensus.io v0.22.1
	golang.org/x/exp v0.0.0-20190927203820-447a159532ef // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e // indirect
	golang.org/x/time v0.0.0-20190921001708-c4c64cad1fd0 // indirect
	gonum.org/v1/gonum v0.0.0-20190911200027-40d3308efe80 // indirect
	google.golang.org/appengine v1.6.4 // indirect
	google.golang.org/genproto v0.0.0-20190927181202-20e1ac93f88c // indirect
	google.golang.org/grpc v1.24.0
	gopkg.in/yaml.v2 v2.2.4
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/cli-runtime v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/code-generator v0.0.0-20190912042602-ebc0eb3a5c23
	k8s.io/gengo v0.0.0-20190907103519-ebc107f98eab // indirect
	k8s.io/klog v1.0.0
	pack.ag/amqp v0.12.3 // indirect
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.0+incompatible
	k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
)
