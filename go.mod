module github.com/dapr/dapr

go 1.15

require (
	contrib.go.opencensus.io/exporter/prometheus v0.1.0
	github.com/AdhityaRamadhanus/fasthttpcors v0.0.0-20170121111917-d4c07198763a
	github.com/armon/go-metrics v0.3.4 // indirect
	github.com/dapr/components-contrib v0.4.1-0.20201015165349-4e77594a4d0f
	github.com/fasthttp/router v1.3.1
	github.com/fatih/color v1.9.0 // indirect
	github.com/fsnotify/fsnotify v1.4.7
	github.com/ghodss/yaml v1.0.0
	github.com/golang/protobuf v1.3.3
	github.com/google/go-cmp v0.5.1
	github.com/google/uuid v1.1.1
	github.com/gorilla/mux v1.7.3
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0
	github.com/hashicorp/go-hclog v0.14.1
	github.com/hashicorp/go-immutable-radix v1.3.0 // indirect
	github.com/hashicorp/go-msgpack v1.1.5
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/raft v1.2.0
	github.com/hashicorp/raft-boltdb v0.0.0-20191021154308-4207f1bf0617
	github.com/json-iterator/go v1.1.9
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/mitchellh/mapstructure v1.3.2
	github.com/phayes/freeport v0.0.0-20171002181615-b8543db493a5
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.4.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.9.1
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.5.1
	github.com/valyala/fasthttp v1.16.0
	github.com/yuin/gopher-lua v0.0.0-20200603152657-dc2b0ca8b37e // indirect
	go.opencensus.io v0.22.3
	go.uber.org/zap v1.13.0 // indirect
	golang.org/x/sys v0.0.0-20201015000850-e3ed0017c211 // indirect
	google.golang.org/genproto v0.0.0-20200122232147-0452cf42e150
	google.golang.org/grpc v1.26.0
	gopkg.in/square/go-jose.v2 v2.5.0 // indirect
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/api v0.17.8
	k8s.io/apiextensions-apiserver v0.17.2
	k8s.io/apimachinery v0.17.8
	k8s.io/cli-runtime v0.17.0
	k8s.io/client-go v0.17.2
	k8s.io/code-generator v0.17.3
	k8s.io/gengo v0.0.0-20200114144118-36b2048a9120 // indirect
	k8s.io/klog v1.0.0
	k8s.io/metrics v0.17.0
	sigs.k8s.io/controller-runtime v0.5.6
	sigs.k8s.io/yaml v1.2.0 // indirect
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.0+incompatible
	k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
)
