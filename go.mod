module github.com/dapr/dapr

go 1.15

require (
	contrib.go.opencensus.io/exporter/prometheus v0.2.0
	contrib.go.opencensus.io/exporter/zipkin v0.1.1
	github.com/AdhityaRamadhanus/fasthttpcors v0.0.0-20170121111917-d4c07198763a
	github.com/ajg/form v1.5.1 // indirect
	github.com/dapr/components-contrib v1.0.0-rc1.0.20201209222832-6079c3b1136f
	github.com/dnaeon/go-vcr v1.1.0 // indirect
	github.com/fasthttp-contrib/websocket v0.0.0-20160511215533-1f3b11f56072 // indirect
	github.com/fasthttp/router v1.3.2
	github.com/fatih/structs v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.4.7
	github.com/gavv/httpexpect v2.0.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/golang/protobuf v1.4.3
	github.com/google/go-cmp v0.5.2
	github.com/google/uuid v1.1.2
	github.com/gorilla/mux v1.7.3
	github.com/grpc-ecosystem/go-grpc-middleware v1.2.2
	github.com/hashicorp/go-hclog v0.9.1
	github.com/hashicorp/go-msgpack v0.5.5
	github.com/hashicorp/raft v1.2.0
	github.com/hashicorp/raft-boltdb v0.0.0-20171010151810-6e5ba93211ea
	github.com/imkira/go-interpol v1.1.0 // indirect
	github.com/json-iterator/go v1.1.10
	github.com/k0kubun/colorstring v0.0.0-20150214042306-9440f1994b88 // indirect
	github.com/kataras/go-errors v0.0.3 // indirect
	github.com/kataras/go-serializer v0.0.4 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/microcosm-cc/bluemonday v1.0.4 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/mitchellh/mapstructure v1.3.3
	github.com/moul/http2curl v1.0.0 // indirect
	github.com/openzipkin/zipkin-go v0.2.2
	github.com/phayes/freeport v0.0.0-20171002181615-b8543db493a5
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.8.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.14.0
	github.com/sirupsen/logrus v1.6.0
	github.com/stretchr/testify v1.6.1
	github.com/valyala/fasthttp v1.16.0
	github.com/xeipuuv/gojsonschema v1.2.0 // indirect
	github.com/yalp/jsonpath v0.0.0-20180802001716-5cc68e5049a0 // indirect
	github.com/yudai/gojsondiff v1.0.0 // indirect
	github.com/yudai/golcs v0.0.0-20170316035057-ecda9a501e82 // indirect
	github.com/yudai/pp v2.0.1+incompatible // indirect
	github.com/yuin/gopher-lua v0.0.0-20200603152657-dc2b0ca8b37e // indirect
	go.opencensus.io v0.22.4
	go.opentelemetry.io/otel v0.13.0
	go.uber.org/atomic v1.5.0
	google.golang.org/genproto v0.0.0-20200904004341-0bd0a958aa1d
	google.golang.org/grpc v1.33.1
	gopkg.in/couchbaselabs/gocbconnstr.v1 v1.0.0-00010101000000-000000000000 // indirect
	gopkg.in/couchbaselabs/jsonx.v1 v1.0.0 // indirect
	gopkg.in/jcmturner/goidentity.v3 v3.0.0 // indirect
	gopkg.in/kataras/go-serializer.v0 v0.0.4 // indirect
	gopkg.in/square/go-jose.v2 v2.5.0 // indirect
	gopkg.in/yaml.v2 v2.3.0
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
	github.com/dapr/components-contrib => github.com/cvictory/components-contrib v1.0.0-rc1.0.20201210065127-ebdd6ab65f19
	gopkg.in/couchbaselabs/gocbconnstr.v1 => github.com/couchbaselabs/gocbconnstr v1.0.5
	k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
)
