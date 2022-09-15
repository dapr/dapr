module github.com/dapr/dapr

go 1.19

require (
	contrib.go.opencensus.io/exporter/prometheus v0.4.1
	github.com/AdhityaRamadhanus/fasthttpcors v0.0.0-20170121111917-d4c07198763a
	github.com/PuerkitoBio/purell v1.1.1
	github.com/agrea/ptr v0.0.0-20180711073057-77a518d99b7b
	github.com/cenkalti/backoff/v4 v4.1.3
	github.com/dapr/components-contrib v1.8.0-rc.1.0.20220826012745-bcea284c7b0c
	github.com/dapr/kit v0.0.2
	github.com/fasthttp/router v1.3.8
	github.com/fsnotify/fsnotify v1.5.4
	github.com/ghodss/yaml v1.0.0
	github.com/go-logr/logr v1.2.3
	github.com/gogo/protobuf v1.3.2
	github.com/google/cel-go v0.9.0
	github.com/google/go-cmp v0.5.8
	github.com/google/uuid v1.3.0
	github.com/gorilla/mux v1.8.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/hashicorp/go-hclog v1.2.1
	github.com/hashicorp/go-msgpack v1.1.5
	github.com/hashicorp/go-multierror v1.1.1
	github.com/hashicorp/golang-lru v0.5.4
	github.com/hashicorp/raft v1.3.9
	github.com/hashicorp/raft-boltdb v0.0.0-20220329195025-15018e9b97e0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/mitchellh/mapstructure v1.5.1-0.20220423185008-bf980b35cac4
	github.com/openzipkin/zipkin-go v0.4.0 // indirect
	github.com/phayes/freeport v0.0.0-20220201140144-74d24b5ae9f5
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.2
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.35.0
	github.com/sony/gobreaker v0.4.2-0.20210216022020-dd874f9dd33b
	github.com/stretchr/testify v1.8.0
	github.com/valyala/fasthttp v1.31.1-0.20211216042702-258a4c17b4f4
	go.opencensus.io v0.23.0
	go.opentelemetry.io/otel v1.7.0
	go.uber.org/atomic v1.9.0
	go.uber.org/automaxprocs v1.5.1
	go.uber.org/ratelimit v0.2.0
	golang.org/x/net v0.0.0-20220722155237-a158d28d115b
	google.golang.org/genproto v0.0.0-20220622171453-ea41d75dfa0f
	google.golang.org/grpc v1.47.0
	google.golang.org/protobuf v1.28.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.25.0
	k8s.io/apiextensions-apiserver v0.23.0
	k8s.io/apimachinery v0.25.0
	k8s.io/cli-runtime v0.23.0
	k8s.io/client-go v0.25.0
	k8s.io/code-generator v0.23.0
	k8s.io/klog v1.0.0
	k8s.io/metrics v0.20.0
	sigs.k8s.io/controller-runtime v0.11.0
)

require (
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.2
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.7.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.7.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.7.0
	go.opentelemetry.io/otel/exporters/zipkin v1.7.0
	go.opentelemetry.io/otel/sdk v1.7.0
	go.opentelemetry.io/otel/trace v1.7.0
)

require (
	cloud.google.com/go v0.100.2 // indirect
	cloud.google.com/go/compute v1.5.0 // indirect
	cloud.google.com/go/datastore v1.1.0 // indirect
	cloud.google.com/go/iam v0.3.0 // indirect
	cloud.google.com/go/pubsub v1.12.2 // indirect
	cloud.google.com/go/secretmanager v1.4.0 // indirect
	cloud.google.com/go/storage v1.10.0 // indirect
	dubbo.apache.org/dubbo-go/v3 v3.0.3-0.20220610080020-48691a404537 // indirect
	github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4 // indirect
	github.com/99designs/keyring v1.2.0 // indirect
	github.com/AthenZ/athenz v1.10.39 // indirect
	github.com/Azure/azure-amqp-common-go/v3 v3.2.3 // indirect
	github.com/Azure/azure-event-hubs-go/v3 v3.3.18 // indirect
	github.com/Azure/azure-pipeline-go v0.2.3 // indirect
	github.com/Azure/azure-sdk-for-go v65.0.0+incompatible // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig v0.4.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos v0.3.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/data/aztables v1.0.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.0.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets v0.7.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/keyvault/internal v0.5.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus v1.0.1 // indirect
	github.com/Azure/azure-storage-blob-go v0.10.0 // indirect
	github.com/Azure/azure-storage-queue-go v0.0.0-20191125232315-636801874cdd // indirect
	github.com/Azure/go-amqp v0.17.4 // indirect
	github.com/Azure/go-autorest v14.2.0+incompatible // indirect
	github.com/Azure/go-autorest/autorest v0.11.27 // indirect
	github.com/Azure/go-autorest/autorest/adal v0.9.20 // indirect
	github.com/Azure/go-autorest/autorest/azure/auth v0.5.11 // indirect
	github.com/Azure/go-autorest/autorest/azure/cli v0.4.5 // indirect
	github.com/Azure/go-autorest/autorest/date v0.3.0 // indirect
	github.com/Azure/go-autorest/autorest/to v0.4.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.3.1 // indirect
	github.com/Azure/go-autorest/logger v0.2.1 // indirect
	github.com/Azure/go-autorest/tracing v0.6.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v0.5.1 // indirect
	github.com/DataDog/zstd v1.5.0 // indirect
	github.com/OneOfOne/xxhash v1.2.8 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/RoaringBitmap/roaring v1.1.0 // indirect
	github.com/Shopify/sarama v1.30.0 // indirect
	github.com/StackExchange/wmi v0.0.0-20210224194228-fe8f1750fd46 // indirect
	github.com/Workiva/go-datastructures v1.0.52 // indirect
	github.com/a8m/documentdb v1.3.1-0.20220405205223-5b41ba0aaeb1 // indirect
	github.com/aerospike/aerospike-client-go v4.5.0+incompatible // indirect
	github.com/afex/hystrix-go v0.0.0-20180502004556-fa1af6a1f4f5 // indirect
	github.com/agnivade/levenshtein v1.0.1 // indirect
	github.com/alibaba/sentinel-golang v1.0.4 // indirect
	github.com/alibabacloud-go/alibabacloud-gateway-spi v0.0.4 // indirect
	github.com/alibabacloud-go/darabonba-openapi v0.1.16 // indirect
	github.com/alibabacloud-go/debug v0.0.0-20190504072949-9472017b5c68 // indirect
	github.com/alibabacloud-go/endpoint-util v1.1.0 // indirect
	github.com/alibabacloud-go/oos-20190601 v1.0.1 // indirect
	github.com/alibabacloud-go/openapi-util v0.0.10 // indirect
	github.com/alibabacloud-go/tea v1.1.17 // indirect
	github.com/alibabacloud-go/tea-utils v1.4.3 // indirect
	github.com/aliyun/aliyun-log-go-sdk v0.1.37 // indirect
	github.com/aliyun/aliyun-oss-go-sdk v2.0.7+incompatible // indirect
	github.com/aliyun/aliyun-tablestore-go-sdk v1.6.0 // indirect
	github.com/aliyun/credentials-go v1.1.2 // indirect
	github.com/andres-erbsen/clock v0.0.0-20160526145045-9e14626cd129 // indirect
	github.com/andybalholm/brotli v1.0.2 // indirect
	github.com/antlr/antlr4/runtime/Go/antlr v0.0.0-20210826220005-b48c857c3a0e // indirect
	github.com/apache/dubbo-getty v1.4.9-0.20220610060150-8af010f3f3dc // indirect
	github.com/apache/dubbo-go-hessian2 v1.11.0 // indirect
	github.com/apache/pulsar-client-go v0.8.1 // indirect
	github.com/apache/pulsar-client-go/oauth2 v0.0.0-20220120090717-25e59572242e // indirect
	github.com/apache/rocketmq-client-go/v2 v2.1.1-rc2 // indirect
	github.com/appscode/go-querystring v0.0.0-20170504095604-0126cfb3f1dc // indirect
	github.com/ardielle/ardielle-go v1.5.2 // indirect
	github.com/armon/go-metrics v0.3.10 // indirect
	github.com/asaskevich/govalidator v0.0.0-20200108200545-475eaeb16496 // indirect
	github.com/aws/aws-sdk-go v1.43.16 // indirect
	github.com/awslabs/kinesis-aggregation/go v0.0.0-20210630091500-54e17340d32f // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.2.0 // indirect
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/bradfitz/gomemcache v0.0.0-20190913173617-a41fca850d0b // indirect
	github.com/camunda/zeebe/clients/go/v8 v8.0.4 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/coreos/go-oidc v2.1.0+incompatible // indirect
	github.com/creasty/defaults v1.5.2 // indirect
	github.com/cyphar/filepath-securejoin v0.2.3 // indirect
	github.com/dancannon/gorethink v4.0.0+incompatible // indirect
	github.com/danieljoos/wincred v1.1.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/deepmap/oapi-codegen v1.8.1 // indirect
	github.com/denisenkom/go-mssqldb v0.0.0-20210411162248-d9abbec934ba // indirect
	github.com/devigned/tab v0.1.1 // indirect
	github.com/dghubble/go-twitter v0.0.0-20190719072343-39e5462e111f // indirect
	github.com/dghubble/oauth1 v0.6.0 // indirect
	github.com/dghubble/sling v1.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/didip/tollbooth v4.0.2+incompatible // indirect
	github.com/dimchansky/utfbom v1.1.1 // indirect
	github.com/dubbogo/gost v1.11.25 // indirect
	github.com/dubbogo/triple v1.1.8 // indirect
	github.com/dvsekhvalnov/jose2go v1.5.0 // indirect
	github.com/eapache/go-resiliency v1.2.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20180814174437-776d5712da21 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/eclipse/paho.mqtt.golang v1.3.5 // indirect
	github.com/emicklei/go-restful/v3 v3.8.0 // indirect
	github.com/emirpasic/gods v1.12.0 // indirect
	github.com/evanphx/json-patch v4.12.0+incompatible // indirect
	github.com/fasthttp-contrib/sessions v0.0.0-20160905201309-74f6ac73d5d5 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/frankban/quicktest v1.14.3 // indirect
	github.com/go-errors/errors v1.4.0 // indirect
	github.com/go-kit/kit v0.10.0 // indirect
	github.com/go-kit/log v0.2.0 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.2.5 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.5 // indirect
	github.com/go-openapi/swag v0.19.14 // indirect
	github.com/go-ozzo/ozzo-validation/v4 v4.3.0 // indirect
	github.com/go-playground/locales v0.14.0 // indirect
	github.com/go-playground/universal-translator v0.18.0 // indirect
	github.com/go-playground/validator/v10 v10.11.0 // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/go-sql-driver/mysql v1.6.0 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/gocql/gocql v0.0.0-20210515062232-b7ef815b4556 // indirect
	github.com/godbus/dbus v0.0.0-20190726142602-4481cbc300e2 // indirect
	github.com/gofrs/uuid v4.0.0+incompatible // indirect
	github.com/golang-jwt/jwt v3.2.2+incompatible // indirect
	github.com/golang-jwt/jwt/v4 v4.2.0 // indirect
	github.com/golang-sql/civil v0.0.0-20190719163853-cb61b32ac6fe // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/googleapis/gax-go/v2 v2.2.0 // indirect
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/gorilla/css v1.0.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/grandcat/zeroconf v0.0.0-20190424104450-85eadb44205c // indirect
	github.com/gregjones/httpcache v0.0.0-20180305231024-9cad4c3443a7 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.7.0 // indirect
	github.com/gsterjov/go-libsecret v0.0.0-20161001094733-a6f4afe4910c // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/hashicorp/consul/api v1.11.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-uuid v1.0.2 // indirect
	github.com/hashicorp/serf v0.9.6 // indirect
	github.com/hazelcast/hazelcast-go-client v0.0.0-20190530123621-6cf767c2f31a // indirect
	github.com/huaweicloud/huaweicloud-sdk-go-obs v3.21.12+incompatible // indirect
	github.com/huaweicloud/huaweicloud-sdk-go-v3 v0.0.87 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/influxdata/influxdb-client-go v1.4.0 // indirect
	github.com/influxdata/line-protocol v0.0.0-20210311194329-9aa0e372d097 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgconn v1.11.0 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.2.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20200714003250-2b9c44734f2b // indirect
	github.com/jackc/pgtype v1.10.0 // indirect
	github.com/jackc/pgx/v4 v4.15.0 // indirect
	github.com/jackc/puddle v1.2.1 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.0.0 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.2 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/jinzhu/copier v0.3.5 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/k0kubun/pp v3.0.1+incompatible // indirect
	github.com/klauspost/compress v1.15.1 // indirect
	github.com/knadh/koanf v1.4.1 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/labd/commercetools-go-sdk v0.3.2 // indirect
	github.com/leodido/go-urn v1.2.1 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/linkedin/goavro/v2 v2.9.8 // indirect
	github.com/machinebox/graphql v0.2.2 // indirect
	github.com/magiconair/properties v1.8.6 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/matoous/go-nanoid/v2 v2.0.0 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-ieproxy v0.0.1 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/miekg/dns v1.1.50 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/mrz1836/postmark v1.2.9 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/mtibben/percent v0.2.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/natefinch/lumberjack v2.0.0+incompatible // indirect
	github.com/nats-io/nats.go v1.13.1-0.20220308171302-2f2f6968e98d // indirect
	github.com/nats-io/nkeys v0.3.0 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/nats-io/stan.go v0.8.3 // indirect
	github.com/open-policy-agent/opa v0.42.0 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/oracle/oci-go-sdk/v54 v54.0.0 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/pelletier/go-toml v1.9.4 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pkg/browser v0.0.0-20210115035449-ce105d075bb4 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/pquerna/cachecontrol v0.0.0-20180517163645-1555304b9b35 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/prometheus/statsd_exporter v0.22.3 // indirect
	github.com/rabbitmq/amqp091-go v1.3.4 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/rs/zerolog v1.25.0 // indirect
	github.com/samuel/go-zookeeper v0.0.0-20190923202752-2cc03de413da // indirect
	github.com/satori/go.uuid v1.2.1-0.20181028125025-b2ce2384e17b // indirect
	github.com/savsgio/gotils v0.0.0-20210217112953-d4a072536008 // indirect
	github.com/sendgrid/rest v2.6.9+incompatible // indirect
	github.com/sendgrid/sendgrid-go v3.11.1+incompatible // indirect
	github.com/shirou/gopsutil v3.20.11+incompatible // indirect
	github.com/shirou/gopsutil/v3 v3.21.6 // indirect
	github.com/sijms/go-ora/v2 v2.2.22 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/cast v1.4.1 // indirect
	github.com/spf13/cobra v1.5.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stathat/consistent v1.0.0 // indirect
	github.com/stoewer/go-strcase v1.2.0 // indirect
	github.com/stretchr/objx v0.4.0 // indirect
	github.com/supplyon/gremcos v0.1.0 // indirect
	github.com/tetratelabs/wazero v0.0.0-20220425003459-ad61d9a6ff43 // indirect
	github.com/tidwall/gjson v1.14.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tjfoc/gmsm v1.3.2 // indirect
	github.com/tklauser/go-sysconf v0.3.6 // indirect
	github.com/tklauser/numcpus v0.2.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/vektah/gqlparser/v2 v2.4.5 // indirect
	github.com/vmware/vmware-go-kcl v1.5.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.0.2 // indirect
	github.com/xdg-go/stringprep v1.0.2 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xlab/treeprint v0.0.0-20181112141820-a009c3971eca // indirect
	github.com/yashtewari/glob-intersection v0.1.0 // indirect
	github.com/youmark/pkcs8 v0.0.0-20181117223130-1be2e3e5546d // indirect
	github.com/yuin/gopher-lua v0.0.0-20200603152657-dc2b0ca8b37e // indirect
	go.mongodb.org/mongo-driver v1.5.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/internal/retry v1.7.0 // indirect
	go.opentelemetry.io/proto/otlp v0.16.0 // indirect
	go.starlark.net v0.0.0-20200306205701-8dd3e2ee1dd5 // indirect
	go.uber.org/multierr v1.8.0 // indirect
	go.uber.org/zap v1.21.0 // indirect
	golang.org/x/crypto v0.0.0-20220525230936-793ad666bf5e // indirect
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4 // indirect
	golang.org/x/oauth2 v0.0.0-20220309155454-6242fa91716a // indirect
	golang.org/x/sync v0.0.0-20220601150217-0de741cfad7f // indirect
	golang.org/x/sys v0.0.0-20220722155257-8c9f86f7a55f // indirect
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211 // indirect
	golang.org/x/text v0.3.7 // indirect
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8 // indirect
	golang.org/x/tools v0.1.11 // indirect
	golang.org/x/xerrors v0.0.0-20220609144429-65e65417b02f // indirect
	gomodules.xyz/jsonpatch/v2 v2.2.0 // indirect
	google.golang.org/api v0.74.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/couchbase/gocb.v1 v1.6.4 // indirect
	gopkg.in/couchbase/gocbcore.v7 v7.1.18 // indirect
	gopkg.in/couchbaselabs/gocbconnstr.v1 v1.0.4 // indirect
	gopkg.in/couchbaselabs/jsonx.v1 v1.0.1 // indirect
	gopkg.in/fatih/pool.v2 v2.0.0 // indirect
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df // indirect
	gopkg.in/gorethink/gorethink.v4 v4.1.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.66.2 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0 // indirect
	gopkg.in/square/go-jose.v2 v2.5.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/component-base v0.23.0 // indirect
	k8s.io/gengo v0.0.0-20210813121822-485abfe95c7c // indirect
	k8s.io/klog/v2 v2.70.1 // indirect
	k8s.io/kube-openapi v0.0.0-20220803162953-67bda5d908f1 // indirect
	k8s.io/utils v0.0.0-20220728103510-ee6ede2d64ed // indirect
	sigs.k8s.io/json v0.0.0-20220713155537-f223a00ba0e2 // indirect
	sigs.k8s.io/kustomize/api v0.10.1 // indirect
	sigs.k8s.io/kustomize/kyaml v0.13.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	github.com/toolkits/concurrent => github.com/niean/gotools v0.0.0-20151221085310-ff3f51fc5c60
	gopkg.in/couchbaselabs/gocbconnstr.v1 => github.com/couchbaselabs/gocbconnstr v1.0.5
	k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
)

replace github.com/eclipse/paho.mqtt.golang => github.com/shivamkm07/paho.mqtt.golang v1.3.6-0.20220106130409-e28a1db639f8

// Uncomment for local development for testing with changes in the components-contrib repository.
// Don't commit with this uncommented!
//
// replace github.com/dapr/components-contrib => ../components-contrib
//
// Then, run `make modtidy` in this repository.
// This ensures that go.mod and go.sum are up-to-date.
