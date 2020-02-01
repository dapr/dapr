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

	"github.com/dapr/dapr/pkg/runtime"
	log "github.com/sirupsen/logrus"

	// Included components in compiled daprd

	// Secret stores
	"github.com/dapr/components-contrib/secretstores/azure/keyvault"
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
	"github.com/dapr/components-contrib/pubsub/azure/servicebus"
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
	servicediscovery_kubernetes "github.com/dapr/components-contrib/servicediscovery/kubernetes"
	"github.com/dapr/components-contrib/servicediscovery/mdns"
	servicediscovery_loader "github.com/dapr/dapr/pkg/components/servicediscovery"

	// Bindings
	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/aws/dynamodb"
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
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/valyala/fasthttp"
)

func main() {
	rt, err := runtime.FromFlags()
	if err != nil {
		log.Fatal(err)
	}

	err = rt.Run(
		runtime.WithSecretStores(
			secretstores_loader.New("kubernetes", sercetstores_kubernetes.NewKubernetesSecretStore),
			secretstores_loader.New("azure.keyvault", keyvault.NewAzureKeyvaultSecretStore),
			secretstores_loader.New("hashicorp.vault", vault.NewHashiCorpVaultSecretStore),
		),
		runtime.WithStates(
			state_loader.New("redis", func() state.Store {
				return state_redis.NewRedisStateStore()
			}),
			state_loader.New("consul", func() state.Store {
				return consul.NewConsulStateStore()
			}),
			state_loader.New("azure.cosmosdb", func() state.Store {
				return state_cosmosdb.NewCosmosDBStateStore()
			}),
			state_loader.New("azure.tablestorage", func() state.Store {
				return state_azure_tablestorage.NewAzureTablesStateStore()
			}),
			state_loader.New("etcd", func() state.Store {
				return etcd.NewETCD()
			}),
			state_loader.New("cassandra", func() state.Store {
				return cassandra.NewCassandraStateStore()
			}),
			state_loader.New("memcached", func() state.Store {
				return memcached.NewMemCacheStateStore()
			}),
			state_loader.New("mongodb", func() state.Store {
				return mongodb.NewMongoDB()
			}),
			state_loader.New("zookeeper", func() state.Store {
				return zookeeper.NewZookeeperStateStore()
			}),
			state_loader.New("gcp.firestore", func() state.Store {
				return firestore.NewFirestoreStateStore()
			}),
			state_loader.New("sqlserver", func() state.Store {
				return sqlserver.NewSQLServerStateStore()
			}),
			state_loader.New("hazelcast", func() state.Store {
				return hazelcast.NewHazelcastStore()
			}),
			state_loader.New("cloudstate.crdt", func() state.Store {
				return cloudstate.NewCRDT()
			}),
			state_loader.New("couchbase", func() state.Store {
				return couchbase.NewCouchbaseStateStore()
			}),
			state_loader.New("aerospike", func() state.Store {
				return aerospike.NewAerospikeStateStore()
			}),
		),
		runtime.WithPubSubs(
			pubsub_loader.New("redis", pubsub_redis.NewRedisStreams),
			pubsub_loader.New("nats", nats.NewNATSPubSub),
			pubsub_loader.New("azure.servicebus", servicebus.NewAzureServiceBus),
			pubsub_loader.New("rabbitmq", rabbitmq.NewRabbitMQ),
		),
		runtime.WithExporters(
			exporters_loader.New("zipkin", func() exporters.Exporter {
				return zipkin.NewZipkinExporter()
			}),
			exporters_loader.New("string", func() exporters.Exporter {
				return stringexporter.NewStringExporter()
			}),
			exporters_loader.New("native", func() exporters.Exporter {
				return native.NewNativeExporter()
			}),
		),
		runtime.WithServiceDiscovery(
			servicediscovery_loader.New("mdns", mdns.NewMDNSResolver),
			servicediscovery_loader.New("kubernetes", servicediscovery_kubernetes.NewKubernetesResolver),
		),
		runtime.WithInputBindings(
			bindings_loader.NewInput("aws.sqs", func() bindings.InputBinding {
				return sqs.NewAWSSQS()
			}),
			bindings_loader.NewInput("azure.eventhubs", func() bindings.InputBinding {
				return eventhubs.NewAzureEventHubs()
			}),
			bindings_loader.NewInput("kafka", func() bindings.InputBinding {
				return kafka.NewKafka()
			}),
			bindings_loader.NewInput("mqtt", func() bindings.InputBinding {
				return mqtt.NewMQTT()
			}),
			bindings_loader.NewInput("rabbitmq", func() bindings.InputBinding {
				return bindings_rabbitmq.NewRabbitMQ()
			}),
			bindings_loader.NewInput("azure.servicebusqueues", func() bindings.InputBinding {
				return servicebusqueues.NewAzureServiceBusQueues()
			}),
			bindings_loader.NewInput("azure.storagequeues", func() bindings.InputBinding {
				return storagequeues.NewAzureStorageQueues()
			}),
			bindings_loader.NewInput("gcp.pubsub", func() bindings.InputBinding {
				return pubsub.NewGCPPubSub()
			}),
			bindings_loader.NewInput("kubernetes", kubernetes.NewKubernetes),
		),
		runtime.WithOutputBindings(
			bindings_loader.NewOutput("aws.sqs", func() bindings.OutputBinding {
				return sqs.NewAWSSQS()
			}),
			bindings_loader.NewOutput("aws.sns", func() bindings.OutputBinding {
				return sns.NewAWSSNS()
			}),
			bindings_loader.NewOutput("azure.eventhubs", func() bindings.OutputBinding {
				return eventhubs.NewAzureEventHubs()
			}),
			bindings_loader.NewOutput("aws.dynamodb", func() bindings.OutputBinding {
				return dynamodb.NewDynamoDB()
			}),
			bindings_loader.NewOutput("azure.cosmosdb", func() bindings.OutputBinding {
				return bindings_cosmosdb.NewCosmosDB()
			}),
			bindings_loader.NewOutput("gcp.bucket", func() bindings.OutputBinding {
				return bucket.NewGCPStorage()
			}),
			bindings_loader.NewOutput("http", func() bindings.OutputBinding {
				return http.NewHTTP()
			}),
			bindings_loader.NewOutput("kafka", func() bindings.OutputBinding {
				return kafka.NewKafka()
			}),
			bindings_loader.NewOutput("mqtt", func() bindings.OutputBinding {
				return mqtt.NewMQTT()
			}),
			bindings_loader.NewOutput("rabbitmq", func() bindings.OutputBinding {
				return bindings_rabbitmq.NewRabbitMQ()
			}),
			bindings_loader.NewOutput("redis", func() bindings.OutputBinding {
				return redis.NewRedis()
			}),
			bindings_loader.NewOutput("aws.s3", func() bindings.OutputBinding {
				return s3.NewAWSS3()
			}),
			bindings_loader.NewOutput("azure.blobstorage", func() bindings.OutputBinding {
				return blobstorage.NewAzureBlobStorage()
			}),
			bindings_loader.NewOutput("azure.servicebusqueues", func() bindings.OutputBinding {
				return servicebusqueues.NewAzureServiceBusQueues()
			}),
			bindings_loader.NewOutput("azure.storagequeues", func() bindings.OutputBinding {
				return storagequeues.NewAzureStorageQueues()
			}),
			bindings_loader.NewOutput("gcp.pubsub", func() bindings.OutputBinding {
				return pubsub.NewGCPPubSub()
			}),
			bindings_loader.NewOutput("azure.signalr", func() bindings.OutputBinding {
				return signalr.NewSignalR()
			}),
			bindings_loader.NewOutput("twilio.sms", func() bindings.OutputBinding {
				return twilio.NewSMS()
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
