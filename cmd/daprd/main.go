// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/kit/logger"

	// Included components in compiled daprd.

	// Secret stores.
	"github.com/dapr/components-contrib/secretstores/aws/parameterstore"
	"github.com/dapr/components-contrib/secretstores/aws/secretmanager"
	"github.com/dapr/components-contrib/secretstores/azure/keyvault"
	gcp_secretmanager "github.com/dapr/components-contrib/secretstores/gcp/secretmanager"
	"github.com/dapr/components-contrib/secretstores/hashicorp/vault"
	sercetstores_kubernetes "github.com/dapr/components-contrib/secretstores/kubernetes"
	secretstore_env "github.com/dapr/components-contrib/secretstores/local/env"
	secretstore_file "github.com/dapr/components-contrib/secretstores/local/file"

	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"

	// State Stores.
	"github.com/dapr/components-contrib/state/aerospike"
	state_dynamodb "github.com/dapr/components-contrib/state/aws/dynamodb"
	state_azure_blobstorage "github.com/dapr/components-contrib/state/azure/blobstorage"
	state_cosmosdb "github.com/dapr/components-contrib/state/azure/cosmosdb"
	state_azure_tablestorage "github.com/dapr/components-contrib/state/azure/tablestorage"
	"github.com/dapr/components-contrib/state/cassandra"
	"github.com/dapr/components-contrib/state/cloudstate"
	"github.com/dapr/components-contrib/state/couchbase"
	"github.com/dapr/components-contrib/state/gcp/firestore"
	"github.com/dapr/components-contrib/state/hashicorp/consul"
	"github.com/dapr/components-contrib/state/hazelcast"
	"github.com/dapr/components-contrib/state/memcached"
	"github.com/dapr/components-contrib/state/mongodb"
	state_mysql "github.com/dapr/components-contrib/state/mysql"
	"github.com/dapr/components-contrib/state/postgresql"
	state_redis "github.com/dapr/components-contrib/state/redis"
	"github.com/dapr/components-contrib/state/rethinkdb"
	"github.com/dapr/components-contrib/state/sqlserver"
	"github.com/dapr/components-contrib/state/zookeeper"

	state_loader "github.com/dapr/dapr/pkg/components/state"

	// Pub/Sub.
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

	// Name resolutions.
	nr_consul "github.com/dapr/components-contrib/nameresolution/consul"
	nr_kubernetes "github.com/dapr/components-contrib/nameresolution/kubernetes"
	nr_mdns "github.com/dapr/components-contrib/nameresolution/mdns"

	nr_loader "github.com/dapr/dapr/pkg/components/nameresolution"

	dingtalk_webhook "github.com/dapr/components-contrib/bindings/alicloud/dingtalk/webhook"
	"github.com/dapr/components-contrib/bindings/alicloud/oss"
	"github.com/dapr/components-contrib/bindings/apns"
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
	"github.com/dapr/components-contrib/bindings/graphql"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/influx"
	"github.com/dapr/components-contrib/bindings/kafka"
	"github.com/dapr/components-contrib/bindings/kubernetes"
	"github.com/dapr/components-contrib/bindings/localstorage"
	"github.com/dapr/components-contrib/bindings/mqtt"
	"github.com/dapr/components-contrib/bindings/mysql"
	"github.com/dapr/components-contrib/bindings/postgres"
	"github.com/dapr/components-contrib/bindings/postmark"
	bindings_rabbitmq "github.com/dapr/components-contrib/bindings/rabbitmq"
	"github.com/dapr/components-contrib/bindings/redis"
	"github.com/dapr/components-contrib/bindings/rethinkdb/statechange"
	"github.com/dapr/components-contrib/bindings/smtp"
	"github.com/dapr/components-contrib/bindings/twilio/sendgrid"
	"github.com/dapr/components-contrib/bindings/twilio/sms"
	"github.com/dapr/components-contrib/bindings/twitter"
	bindings_zeebe_command "github.com/dapr/components-contrib/bindings/zeebe/command"
	bindings_zeebe_jobworker "github.com/dapr/components-contrib/bindings/zeebe/jobworker"

	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"

	// HTTP Middleware.

	middleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/bearer"
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	"github.com/dapr/components-contrib/middleware/http/oauth2clientcredentials"
	"github.com/dapr/components-contrib/middleware/http/opa"
	"github.com/dapr/components-contrib/middleware/http/ratelimit"
	"github.com/dapr/components-contrib/middleware/http/sentinel"

	http_middleware_loader "github.com/dapr/dapr/pkg/components/middleware/http"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
)

var (
	log        = logger.NewLogger("dapr.runtime")
	logContrib = logger.NewLogger("dapr.contrib")
)

func main() {
	logger.DaprVersion = version.Version()
	rt, err := runtime.FromFlags()
	if err != nil {
		log.Fatal(err)
	}

	err = rt.Run(
		runtime.WithSecretStores(
			secretstores_loader.New("kubernetes", sercetstores_kubernetes.NewKubernetesSecretStore(logContrib)),
			secretstores_loader.New("azure.keyvault", keyvault.NewAzureKeyvaultSecretStore(logContrib)),
			secretstores_loader.New("hashicorp.vault", vault.NewHashiCorpVaultSecretStore(logContrib)),
			secretstores_loader.New("aws.secretmanager", secretmanager.NewSecretManager(logContrib)),
			secretstores_loader.New("aws.parameterstore", parameterstore.NewParameterStore(logContrib)),
			secretstores_loader.New("gcp.secretmanager", gcp_secretmanager.NewSecreteManager(logContrib)),
			secretstores_loader.New("local.file", secretstore_file.NewLocalSecretStore(logContrib)),
			secretstores_loader.New("local.env", secretstore_env.NewEnvSecretStore(logContrib)),
		),
		runtime.WithStates(
			state_loader.New("redis", state_redis.NewRedisStateStore(logContrib)),
			state_loader.New("consul", consul.NewConsulStateStore(logContrib)),
			state_loader.New("azure.blobstorage", state_azure_blobstorage.NewAzureBlobStorageStore(logContrib)),
			state_loader.New("azure.cosmosdb", state_cosmosdb.NewCosmosDBStateStore(logContrib)),
			state_loader.New("azure.tablestorage", state_azure_tablestorage.NewAzureTablesStateStore(logContrib)),
			state_loader.New("cassandra", cassandra.NewCassandraStateStore(logContrib)),
			state_loader.New("memcached", memcached.NewMemCacheStateStore(logContrib)),
			state_loader.New("mongodb", mongodb.NewMongoDB(logContrib)),
			state_loader.New("zookeeper", zookeeper.NewZookeeperStateStore(logContrib)),
			state_loader.New("gcp.firestore", firestore.NewFirestoreStateStore(logContrib)),
			state_loader.New("postgresql", postgresql.NewPostgreSQLStateStore(logContrib)),
			state_loader.New("sqlserver", sqlserver.NewSQLServerStateStore(logContrib)),
			state_loader.New("hazelcast", hazelcast.NewHazelcastStore(logContrib)),
			state_loader.New("cloudstate.crdt", cloudstate.NewCRDT(logContrib)),
			state_loader.New("couchbase", couchbase.NewCouchbaseStateStore(logContrib)),
			state_loader.New("aerospike", aerospike.NewAerospikeStateStore(logContrib)),
			state_loader.New("rethinkdb", rethinkdb.NewRethinkDBStateStore(logContrib)),
			state_loader.New("aws.dynamodb", state_dynamodb.NewDynamoDBStateStore()),
			state_loader.New("mysql", state_mysql.NewMySQLStateStore(logContrib)),
		),
		runtime.WithPubSubs(
			pubsub_loader.New("azure.eventhubs", pubsub_eventhubs.NewAzureEventHubs(logContrib)),
			pubsub_loader.New("azure.servicebus", servicebus.NewAzureServiceBus(logContrib)),
			pubsub_loader.New("gcp.pubsub", pubsub_gcp.NewGCPPubSub(logContrib)),
			pubsub_loader.New("hazelcast", pubsub_hazelcast.NewHazelcastPubSub(logContrib)),
			pubsub_loader.New("kafka", pubsub_kafka.NewKafka(logContrib)),
			pubsub_loader.New("mqtt", pubsub_mqtt.NewMQTTPubSub(logContrib)),
			pubsub_loader.New("natsstreaming", natsstreaming.NewNATSStreamingPubSub(logContrib)),
			pubsub_loader.New("pulsar", pubsub_pulsar.NewPulsar(logContrib)),
			pubsub_loader.New("rabbitmq", rabbitmq.NewRabbitMQ(logContrib)),
			pubsub_loader.New("redis", pubsub_redis.NewRedisStreams(logContrib)),
			pubsub_loader.New("snssqs", pubsub_snssqs.NewSnsSqs(logContrib)),
		),
		runtime.WithNameResolutions(
			nr_loader.New("mdns", nr_mdns.NewResolver(logContrib)),
			nr_loader.New("kubernetes", nr_kubernetes.NewResolver(logContrib)),
			nr_loader.New("consul", nr_consul.NewResolver(logContrib)),
		),
		runtime.WithInputBindings(
			bindings_loader.NewInput("aws.sqs", sqs.NewAWSSQS(logContrib)),
			bindings_loader.NewInput("aws.kinesis", kinesis.NewAWSKinesis(logContrib)),
			bindings_loader.NewInput("azure.eventgrid", eventgrid.NewAzureEventGrid(logContrib)),
			bindings_loader.NewInput("azure.eventhubs", eventhubs.NewAzureEventHubs(logContrib)),
			bindings_loader.NewInput("azure.servicebusqueues", servicebusqueues.NewAzureServiceBusQueues(logContrib)),
			bindings_loader.NewInput("azure.storagequeues", storagequeues.NewAzureStorageQueues(logContrib)),
			bindings_loader.NewInput("cron", cron.NewCron(logContrib)),
			bindings_loader.NewInput("dingtalk.webhook", dingtalk_webhook.NewDingTalkWebhook(logContrib)),
			bindings_loader.NewInput("gcp.pubsub", pubsub.NewGCPPubSub(logContrib)),
			bindings_loader.NewInput("kafka", kafka.NewKafka(logContrib)),
			bindings_loader.NewInput("kubernetes", kubernetes.NewKubernetes(logContrib)),
			bindings_loader.NewInput("mqtt", mqtt.NewMQTT(logContrib)),
			bindings_loader.NewInput("rabbitmq", bindings_rabbitmq.NewRabbitMQ(logContrib)),
			bindings_loader.NewInput("rethinkdb.statechange", statechange.NewRethinkDBStateChangeBinding(logContrib)),
			bindings_loader.NewInput("twitter", twitter.NewTwitter(logContrib)),
			bindings_loader.NewInput("zeebe.jobworker", bindings_zeebe_jobworker.NewZeebeJobWorker(logContrib)),
		),
		runtime.WithOutputBindings(
			bindings_loader.NewOutput("alicloud.oss", oss.NewAliCloudOSS(logContrib)),
			bindings_loader.NewOutput("apns", apns.NewAPNS(logContrib)),
			bindings_loader.NewOutput("aws.s3", s3.NewAWSS3(logContrib)),
			bindings_loader.NewOutput("aws.sqs", sqs.NewAWSSQS(logContrib)),
			bindings_loader.NewOutput("aws.sns", sns.NewAWSSNS(logContrib)),
			bindings_loader.NewOutput("aws.kinesis", kinesis.NewAWSKinesis(logContrib)),
			bindings_loader.NewOutput("aws.dynamodb", dynamodb.NewDynamoDB(logContrib)),
			bindings_loader.NewOutput("azure.blobstorage", blobstorage.NewAzureBlobStorage(logContrib)),
			bindings_loader.NewOutput("azure.cosmosdb", bindings_cosmosdb.NewCosmosDB(logContrib)),
			bindings_loader.NewOutput("azure.eventgrid", eventgrid.NewAzureEventGrid(logContrib)),
			bindings_loader.NewOutput("azure.eventhubs", eventhubs.NewAzureEventHubs(logContrib)),
			bindings_loader.NewOutput("azure.servicebusqueues", servicebusqueues.NewAzureServiceBusQueues(logContrib)),
			bindings_loader.NewOutput("azure.signalr", signalr.NewSignalR(logContrib)),
			bindings_loader.NewOutput("azure.storagequeues", storagequeues.NewAzureStorageQueues(logContrib)),
			bindings_loader.NewOutput("cron", cron.NewCron(logContrib)),
			bindings_loader.NewOutput("dingtalk.webhook", dingtalk_webhook.NewDingTalkWebhook(logContrib)),
			bindings_loader.NewOutput("gcp.bucket", bucket.NewGCPStorage(logContrib)),
			bindings_loader.NewOutput("gcp.pubsub", pubsub.NewGCPPubSub(logContrib)),
			bindings_loader.NewOutput("http", http.NewHTTP(logContrib)),
			bindings_loader.NewOutput("influx", influx.NewInflux(logContrib)),
			bindings_loader.NewOutput("kafka", kafka.NewKafka(logContrib)),
			bindings_loader.NewOutput("localstorage", localstorage.NewLocalStorage(logContrib)),
			bindings_loader.NewOutput("mqtt", mqtt.NewMQTT(logContrib)),
			bindings_loader.NewOutput("mysql", mysql.NewMysql(logContrib)),
			bindings_loader.NewOutput("postgres", postgres.NewPostgres(logContrib)),
			bindings_loader.NewOutput("postmark", postmark.NewPostmark(logContrib)),
			bindings_loader.NewOutput("rabbitmq", bindings_rabbitmq.NewRabbitMQ(logContrib)),
			bindings_loader.NewOutput("redis", redis.NewRedis(logContrib)),
			bindings_loader.NewOutput("smtp", smtp.NewSMTP(logContrib)),
			bindings_loader.NewOutput("twilio.sms", sms.NewSMS(logContrib)),
			bindings_loader.NewOutput("twilio.sendgrid", sendgrid.NewSendGrid(logContrib)),
			bindings_loader.NewOutput("twitter", twitter.NewTwitter(logContrib)),
			bindings_loader.NewOutput("zeebe.command", bindings_zeebe_command.NewZeebeCommand(logContrib)),
			bindings_loader.NewOutput("graphql", graphql.NewGraphQL(logContrib)),
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
			http_middleware_loader.New("opa", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := opa.NewMiddleware(log).GetHandler(metadata)
				return handler
			}),
			http_middleware_loader.New("sentinel", func(metadata middleware.Metadata) http_middleware.Middleware {
				handler, _ := sentinel.NewMiddleware(log).GetHandler(metadata)
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
	rt.ShutdownWithWait()
}
