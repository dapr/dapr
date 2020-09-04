// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/runtime"

	// Included components in compiled daprd

	// Secret stores
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/secretstores/aws/secretmanager"
	"github.com/dapr/components-contrib/secretstores/azure/keyvault"
	gcp_secretmanager "github.com/dapr/components-contrib/secretstores/gcp/secretmanager"
	"github.com/dapr/components-contrib/secretstores/hashicorp/vault"
	sercetstores_kubernetes "github.com/dapr/components-contrib/secretstores/kubernetes"
	secretstore_env "github.com/dapr/components-contrib/secretstores/local/env"
	secretstore_file "github.com/dapr/components-contrib/secretstores/local/file"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"

	// State Stores
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/aerospike"
	state_azure_blobstorage "github.com/dapr/components-contrib/state/azure/blobstorage"
	state_cosmosdb "github.com/dapr/components-contrib/state/azure/cosmosdb"
	state_azure_tablestorage "github.com/dapr/components-contrib/state/azure/tablestorage"
	"github.com/dapr/components-contrib/state/cassandra"
	"github.com/dapr/components-contrib/state/cloudstate"
	"github.com/dapr/components-contrib/state/couchbase"
	"github.com/dapr/components-contrib/state/etcd"
	"github.com/dapr/components-contrib/state/gcp/firestore"
	"github.com/dapr/components-contrib/state/hashicorp/consul"
	"github.com/dapr/components-contrib/state/hazelcast"
	"github.com/dapr/components-contrib/state/memcached"
	"github.com/dapr/components-contrib/state/mongodb"
	"github.com/dapr/components-contrib/state/postgresql"
	state_redis "github.com/dapr/components-contrib/state/redis"
	"github.com/dapr/components-contrib/state/sqlserver"
	"github.com/dapr/components-contrib/state/zookeeper"
	state_loader "github.com/dapr/dapr/pkg/components/state"

	// Pub/Sub
	pubs "github.com/dapr/components-contrib/pubsub"
	pubsub_snssqs "github.com/dapr/components-contrib/pubsub/aws/snssqs"
	pubsub_eventhubs "github.com/dapr/components-contrib/pubsub/azure/eventhubs"
	"github.com/dapr/components-contrib/pubsub/azure/servicebus"
	pubsub_gcp "github.com/dapr/components-contrib/pubsub/gcp/pubsub"
	pubsub_hazelcast "github.com/dapr/components-contrib/pubsub/hazelcast"
	pubsub_kafka "github.com/dapr/components-contrib/pubsub/kafka"
	pubsub_mqtt "github.com/dapr/components-contrib/pubsub/mqtt"
	"github.com/dapr/components-contrib/pubsub/natsstreaming"
	pubsub_pulsar "github.com/dapr/components-contrib/pubsub/pulsar"
	"github.com/dapr/components-contrib/pubsub/rabbitmq"
	pubsub_redis "github.com/dapr/components-contrib/pubsub/redis"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"

	// Exporters
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/native"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/components-contrib/exporters/zipkin"
	exporters_loader "github.com/dapr/dapr/pkg/components/exporters"

	// Name resolutions
	nr "github.com/dapr/components-contrib/nameresolution"
	nr_kubernetes "github.com/dapr/components-contrib/nameresolution/kubernetes"
	nr_mdns "github.com/dapr/components-contrib/nameresolution/mdns"
	nr_loader "github.com/dapr/dapr/pkg/components/nameresolution"

	// Bindings
	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/aws/dynamodb"
	"github.com/dapr/components-contrib/bindings/aws/kinesis"
	"github.com/dapr/components-contrib/bindings/aws/s3"
	"github.com/dapr/components-contrib/bindings/aws/sns"
	"github.com/dapr/components-contrib/bindings/aws/sqs"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	bindings_cosmosdb "github.com/dapr/components-contrib/bindings/azure/cosmosdb"
	"github.com/dapr/components-contrib/bindings/azure/eventgrid"
	"github.com/dapr/components-contrib/bindings/azure/eventhubs"
	"github.com/dapr/components-contrib/bindings/azure/servicebusqueues"
	"github.com/dapr/components-contrib/bindings/azure/signalr"
	"github.com/dapr/components-contrib/bindings/azure/storagequeues"
	"github.com/dapr/components-contrib/bindings/cron"
	"github.com/dapr/components-contrib/bindings/gcp/bucket"
	"github.com/dapr/components-contrib/bindings/gcp/pubsub"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/influx"
	"github.com/dapr/components-contrib/bindings/kafka"
	"github.com/dapr/components-contrib/bindings/kubernetes"
	"github.com/dapr/components-contrib/bindings/mqtt"
	bindings_rabbitmq "github.com/dapr/components-contrib/bindings/rabbitmq"
	"github.com/dapr/components-contrib/bindings/redis"
	"github.com/dapr/components-contrib/bindings/twilio/sendgrid"
	"github.com/dapr/components-contrib/bindings/twilio/sms"
	"github.com/dapr/components-contrib/bindings/twitter"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"

	// HTTP Middleware
	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/bearer"
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	"github.com/dapr/components-contrib/middleware/http/oauth2clientcredentials"
	"github.com/dapr/components-contrib/middleware/http/ratelimit"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

var (
	log        = logger.NewLogger("dapr.runtime")
	logContrib = logger.NewLogger("dapr.contrib")
)

func main() {
	rt, err := runtime.FromFlags()
	if err != nil {
		log.Fatal(err)
	}

	err = rt.Run(
		runtime.WithSecretStores(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return sercetstores_kubernetes.NewKubernetesSecretStore(logContrib)
			}),
			secretstores_loader.New("azure.keyvault", func() secretstores.SecretStore {
				return keyvault.NewAzureKeyvaultSecretStore(logContrib)
			}),
			secretstores_loader.New("hashicorp.vault", func() secretstores.SecretStore {
				return vault.NewHashiCorpVaultSecretStore(logContrib)
			}),
			secretstores_loader.New("aws.secretmanager", func() secretstores.SecretStore {
				return secretmanager.NewSecretManager(logContrib)
			}),
			secretstores_loader.New("gcp.secretmanager", func() secretstores.SecretStore {
				return gcp_secretmanager.NewSecreteManager(logContrib)
			}),
			secretstores_loader.New("local.file", func() secretstores.SecretStore {
				return secretstore_file.NewLocalSecretStore(logContrib)
			}),
			secretstores_loader.New("local.env", func() secretstores.SecretStore {
				return secretstore_env.NewEnvSecretStore(logContrib)
			}),
		),
		runtime.WithStates(
			state_loader.New("redis", func() state.Store {
				return state_redis.NewRedisStateStore(logContrib)
			}),
			state_loader.New("consul", func() state.Store {
				return consul.NewConsulStateStore(logContrib)
			}),
			state_loader.New("azure.blobstorage", func() state.Store {
				return state_azure_blobstorage.NewAzureBlobStorageStore(logContrib)
			}),
			state_loader.New("azure.cosmosdb", func() state.Store {
				return state_cosmosdb.NewCosmosDBStateStore(logContrib)
			}),
			state_loader.New("azure.tablestorage", func() state.Store {
				return state_azure_tablestorage.NewAzureTablesStateStore(logContrib)
			}),
			state_loader.New("etcd", func() state.Store {
				return etcd.NewETCD(logContrib)
			}),
			state_loader.New("cassandra", func() state.Store {
				return cassandra.NewCassandraStateStore(logContrib)
			}),
			state_loader.New("memcached", func() state.Store {
				return memcached.NewMemCacheStateStore(logContrib)
			}),
			state_loader.New("mongodb", func() state.Store {
				return mongodb.NewMongoDB(logContrib)
			}),
			state_loader.New("zookeeper", func() state.Store {
				return zookeeper.NewZookeeperStateStore(logContrib)
			}),
			state_loader.New("gcp.firestore", func() state.Store {
				return firestore.NewFirestoreStateStore(logContrib)
			}),
			state_loader.New("postgresql", func() state.Store {
				return postgresql.NewPostgreSQLStateStore(logContrib)
			}),
			state_loader.New("sqlserver", func() state.Store {
				return sqlserver.NewSQLServerStateStore(logContrib)
			}),
			state_loader.New("hazelcast", func() state.Store {
				return hazelcast.NewHazelcastStore(logContrib)
			}),
			state_loader.New("cloudstate.crdt", func() state.Store {
				return cloudstate.NewCRDT(logContrib)
			}),
			state_loader.New("couchbase", func() state.Store {
				return couchbase.NewCouchbaseStateStore(logContrib)
			}),
			state_loader.New("aerospike", func() state.Store {
				return aerospike.NewAerospikeStateStore(logContrib)
			}),
		),
		runtime.WithPubSubs(
			pubsub_loader.New("redis", func() pubs.PubSub {
				return pubsub_redis.NewRedisStreams(logContrib)
			}),
			pubsub_loader.New("natsstreaming", func() pubs.PubSub {
				return natsstreaming.NewNATSStreamingPubSub(logContrib)
			}),
			pubsub_loader.New("azure.eventhubs", func() pubs.PubSub {
				return pubsub_eventhubs.NewAzureEventHubs(logContrib)
			}),
			pubsub_loader.New("azure.servicebus", func() pubs.PubSub {
				return servicebus.NewAzureServiceBus(logContrib)
			}),
			pubsub_loader.New("rabbitmq", func() pubs.PubSub {
				return rabbitmq.NewRabbitMQ(logContrib)
			}),
			pubsub_loader.New("hazelcast", func() pubs.PubSub {
				return pubsub_hazelcast.NewHazelcastPubSub(logContrib)
			}),
			pubsub_loader.New("gcp.pubsub", func() pubs.PubSub {
				return pubsub_gcp.NewGCPPubSub(logContrib)
			}),
			pubsub_loader.New("kafka", func() pubs.PubSub {
				return pubsub_kafka.NewKafka(logContrib)
			}),
			pubsub_loader.New("snssqs", func() pubs.PubSub {
				return pubsub_snssqs.NewSnsSqs(logContrib)
			}),
			pubsub_loader.New("mqtt", func() pubs.PubSub {
				return pubsub_mqtt.NewMQTTPubSub(logContrib)
			}),
			pubsub_loader.New("pulsar", func() pubs.PubSub {
				return pubsub_pulsar.NewPulsar(logContrib)
			}),
		),
		runtime.WithExporters(
			exporters_loader.New("zipkin", func() exporters.Exporter {
				return zipkin.NewZipkinExporter(logContrib)
			}),
			exporters_loader.New("string", func() exporters.Exporter {
				return stringexporter.NewStringExporter(logContrib)
			}),
			exporters_loader.New("native", func() exporters.Exporter {
				return native.NewNativeExporter(logContrib)
			}),
		),
		runtime.WithNameResolutions(
			nr_loader.New("mdns", func() nr.Resolver {
				return nr_mdns.NewResolver(logContrib)
			}),
			nr_loader.New("kubernetes", func() nr.Resolver {
				return nr_kubernetes.NewResolver(logContrib)
			}),
		),
		runtime.WithInputBindings(
			bindings_loader.NewInput("aws.sqs", func() bindings.InputBinding {
				return sqs.NewAWSSQS(logContrib)
			}),
			bindings_loader.NewInput("aws.kinesis", func() bindings.InputBinding {
				return kinesis.NewAWSKinesis(logContrib)
			}),
			bindings_loader.NewInput("azure.eventhubs", func() bindings.InputBinding {
				return eventhubs.NewAzureEventHubs(logContrib)
			}),
			bindings_loader.NewInput("kafka", func() bindings.InputBinding {
				return kafka.NewKafka(logContrib)
			}),
			bindings_loader.NewInput("mqtt", func() bindings.InputBinding {
				return mqtt.NewMQTT(logContrib)
			}),
			bindings_loader.NewInput("rabbitmq", func() bindings.InputBinding {
				return bindings_rabbitmq.NewRabbitMQ(logContrib)
			}),
			bindings_loader.NewInput("azure.servicebusqueues", func() bindings.InputBinding {
				return servicebusqueues.NewAzureServiceBusQueues(logContrib)
			}),
			bindings_loader.NewInput("azure.storagequeues", func() bindings.InputBinding {
				return storagequeues.NewAzureStorageQueues(logContrib)
			}),
			bindings_loader.NewInput("gcp.pubsub", func() bindings.InputBinding {
				return pubsub.NewGCPPubSub(logContrib)
			}),
			bindings_loader.NewInput("kubernetes", func() bindings.InputBinding {
				return kubernetes.NewKubernetes(logContrib)
			}),
			bindings_loader.NewInput("azure.eventgrid", func() bindings.InputBinding {
				return eventgrid.NewAzureEventGrid(logContrib)
			}),
			bindings_loader.NewInput("twitter", func() bindings.InputBinding {
				return twitter.NewTwitter(logContrib)
			}),
			bindings_loader.NewInput("cron", func() bindings.InputBinding {
				return cron.NewCron(logContrib)
			}),
		),
		runtime.WithOutputBindings(
			bindings_loader.NewOutput("aws.sqs", func() bindings.OutputBinding {
				return sqs.NewAWSSQS(logContrib)
			}),
			bindings_loader.NewOutput("aws.sns", func() bindings.OutputBinding {
				return sns.NewAWSSNS(logContrib)
			}),
			bindings_loader.NewOutput("aws.kinesis", func() bindings.OutputBinding {
				return kinesis.NewAWSKinesis(logContrib)
			}),
			bindings_loader.NewOutput("azure.eventhubs", func() bindings.OutputBinding {
				return eventhubs.NewAzureEventHubs(logContrib)
			}),
			bindings_loader.NewOutput("aws.dynamodb", func() bindings.OutputBinding {
				return dynamodb.NewDynamoDB(logContrib)
			}),
			bindings_loader.NewOutput("azure.cosmosdb", func() bindings.OutputBinding {
				return bindings_cosmosdb.NewCosmosDB(logContrib)
			}),
			bindings_loader.NewOutput("gcp.bucket", func() bindings.OutputBinding {
				return bucket.NewGCPStorage(logContrib)
			}),
			bindings_loader.NewOutput("http", func() bindings.OutputBinding {
				return http.NewHTTP(logContrib)
			}),
			bindings_loader.NewOutput("kafka", func() bindings.OutputBinding {
				return kafka.NewKafka(logContrib)
			}),
			bindings_loader.NewOutput("mqtt", func() bindings.OutputBinding {
				return mqtt.NewMQTT(logContrib)
			}),
			bindings_loader.NewOutput("rabbitmq", func() bindings.OutputBinding {
				return bindings_rabbitmq.NewRabbitMQ(logContrib)
			}),
			bindings_loader.NewOutput("redis", func() bindings.OutputBinding {
				return redis.NewRedis(logContrib)
			}),
			bindings_loader.NewOutput("aws.s3", func() bindings.OutputBinding {
				return s3.NewAWSS3(logContrib)
			}),
			bindings_loader.NewOutput("azure.blobstorage", func() bindings.OutputBinding {
				return blobstorage.NewAzureBlobStorage(logContrib)
			}),
			bindings_loader.NewOutput("azure.servicebusqueues", func() bindings.OutputBinding {
				return servicebusqueues.NewAzureServiceBusQueues(logContrib)
			}),
			bindings_loader.NewOutput("azure.storagequeues", func() bindings.OutputBinding {
				return storagequeues.NewAzureStorageQueues(logContrib)
			}),
			bindings_loader.NewOutput("gcp.pubsub", func() bindings.OutputBinding {
				return pubsub.NewGCPPubSub(logContrib)
			}),
			bindings_loader.NewOutput("azure.signalr", func() bindings.OutputBinding {
				return signalr.NewSignalR(logContrib)
			}),
			bindings_loader.NewOutput("twilio.sms", func() bindings.OutputBinding {
				return sms.NewSMS(logContrib)
			}),
			bindings_loader.NewOutput("twilio.sendgrid", func() bindings.OutputBinding {
				return sendgrid.NewSendGrid(logContrib)
			}),
			bindings_loader.NewOutput("azure.eventgrid", func() bindings.OutputBinding {
				return eventgrid.NewAzureEventGrid(logContrib)
			}),
			bindings_loader.NewOutput("cron", func() bindings.OutputBinding {
				return cron.NewCron(logContrib)
			}),
			bindings_loader.NewOutput("twitter", func() bindings.OutputBinding {
				return twitter.NewTwitter(logContrib)
			}),
			bindings_loader.NewOutput("influx", func() bindings.OutputBinding {
				return influx.NewInflux(logContrib)
			}),
		),
		runtime.WithHTTPMiddleware(
			http_middleware_loader.New("uppercase", func(metadata middleware.Metadata) http_middleware.Middleware {
				return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
					return func(ctx *fasthttp.RequestCtx) {
						body := string(ctx.PostBody())
						ctx.Request.SetBody([]byte(strings.ToUpper(body)))
						h(ctx)
					}
				}
			}),
			http_middleware_loader.New("oauth2", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := oauth2.NewOAuth2Middleware().GetHandler(metadata)
				return handler
			}),
			http_middleware_loader.New("oauth2clientcredentials", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := oauth2clientcredentials.NewOAuth2ClientCredentialsMiddleware(log).GetHandler(metadata)
				return handler
			}),
			http_middleware_loader.New("ratelimit", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := ratelimit.NewRateLimitMiddleware(log).GetHandler(metadata)
				return handler
			}),
			http_middleware_loader.New("bearer", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := bearer.NewBearerMiddleware(log).GetHandler(metadata)
				return handler
			}),
		),
	)
	if err != nil {
		log.Fatalf("fatal error from runtime: %s", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	<-stop
	gracefulShutdownDuration := 5 * time.Second
	log.Info("dapr shutting down. Waiting 5 seconds to finish outstanding operations")
	rt.Stop()
	<-time.After(gracefulShutdownDuration)
}
