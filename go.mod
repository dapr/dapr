module github.com/dapr/dapr

go 1.24.6

require (
	contrib.go.opencensus.io/exporter/prometheus v0.4.2
	github.com/PaesslerAG/jsonpath v0.1.1
	github.com/PuerkitoBio/purell v1.2.1
	github.com/aavaz-ai/pii-scrubber v0.0.0-20220812094047-3fa450ab6973
	github.com/alphadose/haxmap v1.4.0
	github.com/argoproj/argo-rollouts v1.4.1
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/cloudevents/sdk-go/v2 v2.15.2
	github.com/coreos/go-oidc/v3 v3.14.1
	github.com/dapr/components-contrib v1.16.0
	github.com/dapr/durabletask-go v0.9.0
	github.com/dapr/kit v0.16.1
	github.com/diagridio/go-etcd-cron v0.9.1
	github.com/evanphx/json-patch/v5 v5.9.0
	github.com/go-chi/chi/v5 v5.2.2
	github.com/go-chi/cors v1.2.1
	github.com/go-logr/logr v1.4.2
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.4
	github.com/google/cel-go v0.20.1
	github.com/google/go-cmp v0.7.0
	github.com/google/gofuzz v1.2.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/grafana/k6-operator v0.0.8
	github.com/grpc-ecosystem/go-grpc-middleware v1.4.0
	github.com/hashicorp/go-hclog v1.6.3
	github.com/hashicorp/go-msgpack/v2 v2.1.1
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/hashicorp/raft v1.4.0
	github.com/hashicorp/raft-boltdb v0.0.0-20230125174641-2a8082862702
	github.com/jackc/pgx/v5 v5.7.4
	github.com/jhump/protoreflect v1.15.3
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/lestrrat-go/jwx/v2 v2.0.21
	github.com/mitchellh/mapstructure v1.5.1-0.20220423185008-bf980b35cac4
	github.com/phayes/freeport v0.0.0-20220201140144-74d24b5ae9f5
	github.com/prometheus/client_golang v1.22.0
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.64.0
	github.com/redis/go-redis/v9 v9.6.3
	github.com/sony/gobreaker v0.5.0
	github.com/spf13/cast v1.8.0
	github.com/spf13/pflag v1.0.6
	github.com/spiffe/go-spiffe/v2 v2.5.0
	github.com/stretchr/testify v1.10.0
	github.com/tmc/langchaingo v0.1.13
	go.etcd.io/etcd/api/v3 v3.5.21
	go.etcd.io/etcd/client/pkg/v3 v3.5.21
	go.etcd.io/etcd/client/v3 v3.5.21
	go.etcd.io/etcd/server/v3 v3.5.21
	go.mongodb.org/mongo-driver v1.14.0
	go.opencensus.io v0.24.0
	go.opentelemetry.io/otel v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.35.0
	go.opentelemetry.io/otel/exporters/zipkin v1.34.0
	go.opentelemetry.io/otel/sdk v1.35.0
	go.opentelemetry.io/otel/trace v1.35.0
	go.uber.org/automaxprocs v1.6.0
	go.uber.org/ratelimit v0.3.0
	golang.org/x/crypto v0.39.0
	golang.org/x/net v0.41.0
	golang.org/x/oauth2 v0.30.0
	golang.org/x/sync v0.15.0
	google.golang.org/genproto/googleapis/api v0.0.0-20250512202823-5a2f75b736a9
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250603155806-513f23925822
	google.golang.org/grpc v1.73.0
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.31.0
	k8s.io/apiextensions-apiserver v0.31.0
	k8s.io/apimachinery v0.33.0
	k8s.io/cli-runtime v0.30.2
	k8s.io/client-go v0.31.0
	k8s.io/code-generator v0.31.0
	k8s.io/klog v1.0.0
	k8s.io/metrics v0.30.2
	k8s.io/utils v0.0.0-20250502105355-0f33e8f1c979
	modernc.org/sqlite v1.34.5
	sigs.k8s.io/controller-runtime v0.19.0
	sigs.k8s.io/yaml v1.4.0
)

require (
	cel.dev/expr v0.23.0 // indirect
	cloud.google.com/go v0.120.0 // indirect
	cloud.google.com/go/auth v0.16.1 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/datastore v1.20.0 // indirect
	cloud.google.com/go/iam v1.5.2 // indirect
	cloud.google.com/go/monitoring v1.24.2 // indirect
	cloud.google.com/go/pubsub v1.49.0 // indirect
	cloud.google.com/go/secretmanager v1.14.7 // indirect
	cloud.google.com/go/storage v1.50.0 // indirect
	dubbo.apache.org/dubbo-go/v3 v3.0.3-0.20230118042253-4f159a2b38f3 // indirect
	github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4 // indirect
	github.com/99designs/keyring v1.2.1 // indirect
	github.com/AthenZ/athenz v1.10.39 // indirect
	github.com/Azure/azure-sdk-for-go v68.0.0+incompatible // indirect
	github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai v0.6.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.11.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.7.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos v1.0.3 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/aztables v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.8.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs v1.2.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus v1.7.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2 v2.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub v1.2.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/internal v1.0.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.3.2 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue v1.0.0 // indirect
	github.com/Azure/go-amqp v1.0.5 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.2.2 // indirect
	github.com/Code-Hex/go-generics-cache v1.3.1 // indirect
	github.com/DataDog/zstd v1.5.2 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.27.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.50.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.50.0 // indirect
	github.com/IBM/sarama v1.45.2 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/RoaringBitmap/roaring v1.1.0 // indirect
	github.com/Workiva/go-datastructures v1.0.53 // indirect
	github.com/aerospike/aerospike-client-go/v6 v6.12.0 // indirect
	github.com/afex/hystrix-go v0.0.0-20180502004556-fa1af6a1f4f5 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/alibaba/sentinel-golang v1.0.4 // indirect
	github.com/alibabacloud-go/alibabacloud-gateway-spi v0.0.4 // indirect
	github.com/alibabacloud-go/darabonba-openapi v0.2.1 // indirect
	github.com/alibabacloud-go/debug v0.0.0-20190504072949-9472017b5c68 // indirect
	github.com/alibabacloud-go/endpoint-util v1.1.0 // indirect
	github.com/alibabacloud-go/oos-20190601 v1.0.4 // indirect
	github.com/alibabacloud-go/openapi-util v0.0.11 // indirect
	github.com/alibabacloud-go/tea v1.2.1 // indirect
	github.com/alibabacloud-go/tea-utils v1.4.5 // indirect
	github.com/alibabacloud-go/tea-xml v1.1.2 // indirect
	github.com/aliyun/aliyun-log-go-sdk v0.1.54 // indirect
	github.com/aliyun/aliyun-oss-go-sdk v2.2.9+incompatible // indirect
	github.com/aliyun/aliyun-tablestore-go-sdk v1.7.10 // indirect
	github.com/aliyun/credentials-go v1.1.2 // indirect
	github.com/aliyunmq/mq-http-go-sdk v1.0.3 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/anshal21/go-worker v1.1.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.0 // indirect
	github.com/apache/dubbo-getty v1.4.9-0.20220610060150-8af010f3f3dc // indirect
	github.com/apache/dubbo-go-hessian2 v1.11.5 // indirect
	github.com/apache/pulsar-client-go v0.14.0 // indirect
	github.com/apache/rocketmq-client-go v1.2.5 // indirect
	github.com/apache/rocketmq-client-go/v2 v2.1.2-0.20230412142645-25003f6f083d // indirect
	github.com/apache/thrift v0.13.0 // indirect
	github.com/ardielle/ardielle-go v1.5.2 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/aws/aws-msk-iam-sasl-signer-go v1.0.1-0.20241125194140-078c08b8574a // indirect
	github.com/aws/aws-sdk-go v1.55.6 // indirect
	github.com/aws/aws-sdk-go-v2 v1.36.5 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.11 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.29.17 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.70 // indirect
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.19.3 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.32 // indirect
	github.com/aws/aws-sdk-go-v2/feature/rds/auth v1.3.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.17.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.43.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.25.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.10.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/servicediscovery v1.32.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.34.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.38.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.25.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.30.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.34.0 // indirect
	github.com/aws/rolesanywhere-credential-helper v1.0.4 // indirect
	github.com/aws/smithy-go v1.22.5 // indirect
	github.com/awslabs/kinesis-aggregation/go v0.0.0-20210630091500-54e17340d32f // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.4.0 // indirect
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/bradfitz/gomemcache v0.0.0-20230905024940-24af94b03874 // indirect
	github.com/bufbuild/protocompile v0.6.0 // indirect
	github.com/bytedance/gopkg v0.0.0-20240711085056-a03554c296f8 // indirect
	github.com/camunda/zeebe/clients/go/v8 v8.2.12 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chebyrash/promise v0.0.0-20230709133807-42ec49ba1459 // indirect
	github.com/choleraehyq/pid v0.0.20 // indirect
	github.com/cinience/go_rocketmq v0.0.2 // indirect
	github.com/clbanning/mxj/v2 v2.5.6 // indirect
	github.com/cloudevents/sdk-go/binding/format/protobuf/v2 v2.14.0 // indirect
	github.com/cloudwego/fastpb v0.0.4-0.20230131074846-6fc453d58b96 // indirect
	github.com/cloudwego/frugal v0.2.0 // indirect
	github.com/cloudwego/gopkg v0.1.0 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/cloudwego/kitex v0.5.0 // indirect
	github.com/cloudwego/netpoll v0.3.2 // indirect
	github.com/cloudwego/thriftgo v0.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20250326154945-ae57f3c0d45f // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/creasty/defaults v1.5.2 // indirect
	github.com/cyphar/filepath-securejoin v0.2.4 // indirect
	github.com/dancannon/gorethink v4.0.0+incompatible // indirect
	github.com/danieljoos/wincred v1.1.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.2.0 // indirect
	github.com/deepmap/oapi-codegen v1.11.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/didip/tollbooth/v7 v7.0.1 // indirect
	github.com/dlclark/regexp2 v1.10.0 // indirect
	github.com/dubbogo/gost v1.13.1 // indirect
	github.com/dubbogo/triple v1.1.8 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/dvsekhvalnov/jose2go v1.6.0 // indirect
	github.com/eapache/go-resiliency v1.7.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eclipse/paho.mqtt.golang v1.4.3 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.32.4 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/evanphx/json-patch v5.7.0+incompatible // indirect
	github.com/fasthttp-contrib/sessions v0.0.0-20160905201309-74f6ac73d5d5 // indirect
	github.com/fatih/color v1.17.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gage-technologies/mistral-go v1.1.0 // indirect
	github.com/go-errors/errors v1.4.2 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.5 // indirect
	github.com/go-kit/kit v0.10.0 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-ozzo/ozzo-validation/v4 v4.3.0 // indirect
	github.com/go-pkgz/expirable-cache v0.1.0 // indirect
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/go-playground/validator/v10 v10.11.0 // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/go-resty/resty/v2 v2.11.0 // indirect
	github.com/go-sql-driver/mysql v1.7.1 // indirect
	github.com/go-zookeeper/zk v1.0.3 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/gocql/gocql v1.5.2 // indirect
	github.com/godbus/dbus v0.0.0-20190726142602-4481cbc300e2 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/gogap/errors v0.0.0-20200228125012-531a6449b28c // indirect
	github.com/gogap/stack v0.0.0-20150131034635-fef68dddd4f8 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/flatbuffers v25.2.10+incompatible // indirect
	github.com/google/gnostic-models v0.6.9 // indirect
	github.com/google/pprof v0.0.0-20241029153458-d1b30febd7db // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.14.1 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.16.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3 // indirect
	github.com/gsterjov/go-libsecret v0.0.0-20161001094733-a6f4afe4910c // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hamba/avro/v2 v2.28.0 // indirect
	github.com/hashicorp/consul/api v1.25.1 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-msgpack v0.5.5 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hazelcast/hazelcast-go-client v0.0.0-20190530123621-6cf767c2f31a // indirect
	github.com/http-wasm/http-wasm-host-go v0.6.0 // indirect
	github.com/huaweicloud/huaweicloud-sdk-go-obs v3.23.4+incompatible // indirect
	github.com/huaweicloud/huaweicloud-sdk-go-v3 v0.1.56 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/influxdata/influxdb-client-go/v2 v2.12.3 // indirect
	github.com/influxdata/line-protocol v0.0.0-20210922203350-b1ad95c89adf // indirect
	github.com/jackc/pgerrcode v0.0.0-20220416144525-469b46aa5efa // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jinzhu/copier v0.3.5 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/k0kubun/pp v3.0.1+incompatible // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/knadh/koanf v1.4.1 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/kubemq-io/kubemq-go v1.7.9 // indirect
	github.com/kubemq-io/protobuf v1.3.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/labd/commercetools-go-sdk v1.3.1 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/lestrrat-go/blackmagic v1.0.2 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc v1.0.5 // indirect
	github.com/lestrrat-go/iter v1.0.2 // indirect
	github.com/lestrrat-go/option v1.0.1 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/linkedin/goavro/v2 v2.14.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/machinebox/graphql v0.2.2 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matoous/go-nanoid/v2 v2.0.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/microsoft/go-mssqldb v1.6.0 // indirect
	github.com/miekg/dns v1.1.57 // indirect
	github.com/mikeee/aws_credential_helper v0.0.1-alpha.2 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/montanaflynn/stats v0.7.0 // indirect
	github.com/mrz1836/postmark v1.6.1 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/mtibben/percent v0.2.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/natefinch/lumberjack v2.0.0+incompatible // indirect
	github.com/nats-io/nats.go v1.31.0 // indirect
	github.com/nats-io/nkeys v0.4.6 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/open-policy-agent/opa v1.4.2 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/openzipkin/zipkin-go v0.4.3 // indirect
	github.com/oracle/coherence-go-client/v2 v2.2.0 // indirect
	github.com/oracle/oci-go-sdk/v54 v54.0.0 // indirect
	github.com/panjf2000/ants/v2 v2.8.1 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.7 // indirect
	github.com/pkoukk/tiktoken-go v0.1.6 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/power-devops/perfstat v0.0.0-20210106213030-5aafc221ea8c // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/prometheus/statsd_exporter v0.22.7 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.0.0 // indirect
	github.com/rabbitmq/amqp091-go v1.9.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20250401214520-65e299d6c5c9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/riferrei/srclient v0.7.2 // indirect
	github.com/rs/zerolog v1.31.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 // indirect
	github.com/segmentio/asm v1.2.0 // indirect
	github.com/sendgrid/rest v2.6.9+incompatible // indirect
	github.com/sendgrid/sendgrid-go v3.13.0+incompatible // indirect
	github.com/shirou/gopsutil/v3 v3.23.12 // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/sijms/go-ora/v2 v2.8.22 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/soheilhy/cmux v0.1.5 // indirect
	github.com/sourcegraph/conc v0.3.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/cobra v1.9.1 // indirect
	github.com/stealthrocket/wasi-go v0.8.1-0.20230912180546-8efbab50fb58 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/supplyon/gremcos v0.1.40 // indirect
	github.com/tchap/go-patricia/v2 v2.3.2 // indirect
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common v1.0.732 // indirect
	github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssm v1.0.732 // indirect
	github.com/tetratelabs/wazero v1.7.0 // indirect
	github.com/tidwall/gjson v1.17.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/transform v0.0.0-20201103190739-32f242e2dbde // indirect
	github.com/tjfoc/gmsm v1.3.2 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/tmc/grpc-websocket-proxy v0.0.0-20220101234140-673ab2c3ae75 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.53.0 // indirect
	github.com/vmware/vmware-go-kcl v1.5.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xiang90/probing v0.0.0-20221125231312-a49e3df8f510 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	github.com/yashtewari/glob-intersection v0.2.0 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	github.com/yuin/gopher-lua v1.1.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	github.com/zeebo/errs v1.4.0 // indirect
	go.etcd.io/bbolt v1.4.0 // indirect
	go.etcd.io/etcd/client/v2 v2.305.21 // indirect
	go.etcd.io/etcd/pkg/v3 v3.5.21 // indirect
	go.etcd.io/etcd/raft/v3 v3.5.21 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.35.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.60.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.35.0 // indirect
	go.opentelemetry.io/proto/otlp v1.6.0 // indirect
	go.starlark.net v0.0.0-20230525235612-a134d8f9ddca // indirect
	go.uber.org/atomic v1.10.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/arch v0.10.0 // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/mod v0.25.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/term v0.32.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.33.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/api v0.231.0 // indirect
	google.golang.org/genproto v0.0.0-20250512202823-5a2f75b736a9 // indirect
	google.golang.org/grpc/examples v0.0.0-20230224211313-3775f633ce20 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/couchbase/gocb.v1 v1.6.7 // indirect
	gopkg.in/couchbase/gocbcore.v7 v7.1.18 // indirect
	gopkg.in/couchbaselabs/gocbconnstr.v1 v1.0.4 // indirect
	gopkg.in/couchbaselabs/jsonx.v1 v1.0.1 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/evanphx/json-patch.v5 v5.6.0 // indirect
	gopkg.in/fatih/pool.v2 v2.0.0 // indirect
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df // indirect
	gopkg.in/gorethink/gorethink.v4 v4.1.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/gengo/v2 v2.0.0-20240826214909-a7b603a56eb7 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff // indirect
	modernc.org/libc v1.61.9 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.8.2 // indirect
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/kustomize/api v0.15.0 // indirect
	sigs.k8s.io/kustomize/kyaml v0.15.0 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
	stathat.com/c/consistent v1.0.0 // indirect
)

replace (
	// this is a fork which addresses a performance issues due to go routines
	dubbo.apache.org/dubbo-go/v3 => dubbo.apache.org/dubbo-go/v3 v3.0.3-0.20230118042253-4f159a2b38f3

	gopkg.in/couchbaselabs/gocbconnstr.v1 => github.com/couchbaselabs/gocbconnstr v1.0.5
)

// the following lines are necessary to update to commits with proper licenses
replace (
	github.com/chenzhuoyu/iasm => github.com/chenzhuoyu/iasm v0.9.0
	github.com/chzyer/logex => github.com/chzyer/logex v1.2.1
	github.com/gobwas/pool => github.com/gobwas/pool v0.2.1
	github.com/toolkits/concurrent => github.com/niean/gotools v0.0.0-20151221085310-ff3f51fc5c60
)

// update retracted indirect dependencies if necessary
// check for retracted versions: go list -mod=mod -f '{{if .Retracted}}{{.}}{{end}}' -u -m all

// Uncomment for local development for testing with changes in the components-contrib && kit repositories.
// Don't commit with this uncommented!
//
// replace github.com/dapr/components-contrib => ../components-contrib
// replace github.com/dapr/kit => ../kit
//
// Then, run `make modtidy-all` in this repository.
// This ensures that go.mod and go.sum are up-to-date for each go.mod file.
