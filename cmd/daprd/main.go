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
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"

	// State Stores
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/aerospike"
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
	state_redis "github.com/dapr/components-contrib/state/redis"
	"github.com/dapr/components-contrib/state/sqlserver"
	"github.com/dapr/components-contrib/state/zookeeper"
	state_loader "github.com/dapr/dapr/pkg/components/state"

	// Pub/Sub
	pubs "github.com/dapr/components-contrib/pubsub"
	pubsub_eventhubs "github.com/dapr/components-contrib/pubsub/azure/eventhubs"
	"github.com/dapr/components-contrib/pubsub/azure/servicebus"
	pubsub_gcp "github.com/dapr/components-contrib/pubsub/gcp/pubsub"
	pubsub_hazelcast "github.com/dapr/components-contrib/pubsub/hazelcast"
	"github.com/dapr/components-contrib/pubsub/nats"
	"github.com/dapr/components-contrib/pubsub/rabbitmq"
	pubsub_redis "github.com/dapr/components-contrib/pubsub/redis"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"

	// Exporters
	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/native"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/components-contrib/exporters/zipkin"
	exporters_loader "github.com/dapr/dapr/pkg/components/exporters"

	// Service Discovery
	"github.com/dapr/components-contrib/servicediscovery"
	servicediscovery_kubernetes "github.com/dapr/components-contrib/servicediscovery/kubernetes"
	"github.com/dapr/components-contrib/servicediscovery/mdns"
	servicediscovery_loader "github.com/dapr/dapr/pkg/components/servicediscovery"

	// Bindings
	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/aws/dynamodb"
	"github.com/dapr/components-contrib/bindings/aws/kinesis"
	"github.com/dapr/components-contrib/bindings/aws/s3"
	"github.com/dapr/components-contrib/bindings/aws/sns"
	"github.com/dapr/components-contrib/bindings/aws/sqs"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	bindings_cosmosdb "github.com/dapr/components-contrib/bindings/azure/cosmosdb"
	"github.com/dapr/components-contrib/bindings/azure/eventhubs"
	"github.com/dapr/components-contrib/bindings/azure/servicebusqueues"
	"github.com/dapr/components-contrib/bindings/azure/signalr"
	"github.com/dapr/components-contrib/bindings/azure/storagequeues"
	"github.com/dapr/components-contrib/bindings/gcp/bucket"
	"github.com/dapr/components-contrib/bindings/gcp/pubsub"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/kafka"
	"github.com/dapr/components-contrib/bindings/kubernetes"
	"github.com/dapr/components-contrib/bindings/mqtt"
	bindings_rabbitmq "github.com/dapr/components-contrib/bindings/rabbitmq"
	"github.com/dapr/components-contrib/bindings/redis"
	"github.com/dapr/components-contrib/bindings/twilio"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"

	// HTTP Middleware
	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/bearer"
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	"github.com/dapr/components-contrib/middleware/http/ratelimit"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

var log = logger.NewLogger("dapr.runtime")

func main() {
	rt, err := runtime.FromFlags()
	if err != nil {
		log.Fatal(err)
	}

	var logContrib = logger.NewLogger("dapr.contrib")

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
		),
		runtime.WithStates(
			state_loader.New("redis", func() state.Store {
				return state_redis.NewRedisStateStore(logContrib)
			}),
			state_loader.New("consul", func() state.Store {
				return consul.NewConsulStateStore(logContrib)
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
			pubsub_loader.New("nats", func() pubs.PubSub {
				return nats.NewNATSPubSub(logContrib)
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
		runtime.WithServiceDiscovery(
			servicediscovery_loader.New("mdns", func() servicediscovery.Resolver {
				return mdns.NewMDNSResolver(logContrib)
			}),
			servicediscovery_loader.New("kubernetes", func() servicediscovery.Resolver {
				return servicediscovery_kubernetes.NewKubernetesResolver(logContrib)
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
				return twilio.NewSMS(logContrib)
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
