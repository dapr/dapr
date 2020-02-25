module github.com/dapr/dapr

go 1.13

require (
	github.com/AdhityaRamadhanus/fasthttpcors v0.0.0-20170121111917-d4c07198763a
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/dapr/components-contrib v0.0.0-20200219164914-5b75f4d0fbc6
	github.com/dapr/go-sdk v0.0.0-20200121181907-48249cda2fad
	github.com/eapache/go-resiliency v1.2.0 // indirect
	github.com/emicklei/go-restful v2.10.0+incompatible // indirect
	github.com/frankban/quicktest v1.5.0 // indirect
	github.com/fsnotify/fsnotify v1.4.7
	github.com/ghodss/yaml v1.0.0
	github.com/go-chi/chi v4.0.3+incompatible // indirect
	github.com/go-ozzo/ozzo-routing v2.1.4+incompatible // indirect
	github.com/gogo/protobuf v1.3.0 // indirect
	github.com/golang/gddo v0.0.0-20190815223733-287de01127ef // indirect
	github.com/golang/mock v1.4.0
	github.com/golang/protobuf v1.3.3
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/gorilla/mux v1.7.3
	github.com/grandcat/zeroconf v0.0.0-20190424104450-85eadb44205c
	github.com/grpc-ecosystem/go-grpc-middleware v1.1.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/improbable-eng/go-httpwares v0.0.0-20191126155631-6144c42a79c9
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/json-iterator/go v1.1.8
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/mitchellh/mapstructure v1.1.2
	github.com/phayes/freeport v0.0.0-20171002181615-b8543db493a5
	github.com/prometheus/client_golang v1.2.1
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.9.1
	github.com/prometheus/procfs v0.0.8 // indirect
	github.com/qiangxue/fasthttp-routing v0.0.0-20160225050629-6ccdc2a18d87
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/valyala/fasthttp v1.6.0
	go.opencensus.io v0.22.3
	golang.org/x/net v0.0.0-20200202094626-16171245cfb2 // indirect
	golang.org/x/sys v0.0.0-20200202164722-d101bd2416d5 // indirect
	google.golang.org/grpc v1.26.0
	gopkg.in/yaml.v2 v2.2.7
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/cli-runtime v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/code-generator v0.0.0-20190912042602-ebc0eb3a5c23
	k8s.io/klog v1.0.0
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.0+incompatible
	github.com/dapr/dapr => /home/jjcollinge/code/go/src/github.com/jjcollinge/dapr
	k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
)
