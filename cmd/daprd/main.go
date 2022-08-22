/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

//nolint:nosnakecase
package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	lockLoader "github.com/dapr/dapr/pkg/components/lock"

	"github.com/valyala/fasthttp"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/dapr/components-contrib/state/zookeeper"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/runtime"

	// Included components in compiled daprd.

	// Secret stores.
	"github.com/dapr/components-contrib/secretstores"
	alicloud_paramstore "github.com/dapr/components-contrib/secretstores/alicloud/parameterstore"
	"github.com/dapr/components-contrib/secretstores/aws/parameterstore"
	"github.com/dapr/components-contrib/secretstores/aws/secretmanager"
	"github.com/dapr/components-contrib/secretstores/azure/keyvault"
	gcp_secretmanager "github.com/dapr/components-contrib/secretstores/gcp/secretmanager"
	"github.com/dapr/components-contrib/secretstores/hashicorp/vault"
	"github.com/dapr/components-contrib/secretstores/huaweicloud/csms"
	secretstore_kubernetes "github.com/dapr/components-contrib/secretstores/kubernetes"
	secretstore_env "github.com/dapr/components-contrib/secretstores/local/env"
	secretstore_file "github.com/dapr/components-contrib/secretstores/local/file"
	"github.com/dapr/components-contrib/secretstores/tencentcloud/ssm"

	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"

	// State Stores.
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/aerospike"
	state_dynamodb "github.com/dapr/components-contrib/state/aws/dynamodb"
	state_azure_blobstorage "github.com/dapr/components-contrib/state/azure/blobstorage"
	state_cosmosdb "github.com/dapr/components-contrib/state/azure/cosmosdb"
	state_azure_tablestorage "github.com/dapr/components-contrib/state/azure/tablestorage"
	"github.com/dapr/components-contrib/state/cassandra"
	"github.com/dapr/components-contrib/state/cockroachdb"
	"github.com/dapr/components-contrib/state/couchbase"
	"github.com/dapr/components-contrib/state/gcp/firestore"
	"github.com/dapr/components-contrib/state/hashicorp/consul"
	"github.com/dapr/components-contrib/state/hazelcast"
	state_in_memory "github.com/dapr/components-contrib/state/in-memory"
	state_jetstream "github.com/dapr/components-contrib/state/jetstream"
	"github.com/dapr/components-contrib/state/memcached"
	"github.com/dapr/components-contrib/state/mongodb"
	state_mysql "github.com/dapr/components-contrib/state/mysql"
	state_oci_objectstorage "github.com/dapr/components-contrib/state/oci/objectstorage"
	state_oracledatabase "github.com/dapr/components-contrib/state/oracledatabase"
	"github.com/dapr/components-contrib/state/postgresql"
	state_redis "github.com/dapr/components-contrib/state/redis"
	"github.com/dapr/components-contrib/state/rethinkdb"
	"github.com/dapr/components-contrib/state/sqlserver"

	stateLoader "github.com/dapr/dapr/pkg/components/state"

	// Pub/Sub.
	pubs "github.com/dapr/components-contrib/pubsub"
	pubsub_aws_snssqs "github.com/dapr/components-contrib/pubsub/aws/snssqs"
	pubsub_eventhubs "github.com/dapr/components-contrib/pubsub/azure/eventhubs"
	"github.com/dapr/components-contrib/pubsub/azure/servicebus"
	pubsub_gcp "github.com/dapr/components-contrib/pubsub/gcp/pubsub"
	pubsub_hazelcast "github.com/dapr/components-contrib/pubsub/hazelcast"
	pubsub_inmemory "github.com/dapr/components-contrib/pubsub/in-memory"
	pubsub_jetstream "github.com/dapr/components-contrib/pubsub/jetstream"
	pubsub_kafka "github.com/dapr/components-contrib/pubsub/kafka"
	pubsub_mqtt "github.com/dapr/components-contrib/pubsub/mqtt"
	"github.com/dapr/components-contrib/pubsub/natsstreaming"
	pubsub_pulsar "github.com/dapr/components-contrib/pubsub/pulsar"
	"github.com/dapr/components-contrib/pubsub/rabbitmq"
	pubsub_redis "github.com/dapr/components-contrib/pubsub/redis"
	"github.com/dapr/components-contrib/pubsub/rocketmq"

	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"

	// Name resolutions.
	nr "github.com/dapr/components-contrib/nameresolution"
	nr_consul "github.com/dapr/components-contrib/nameresolution/consul"
	nr_kubernetes "github.com/dapr/components-contrib/nameresolution/kubernetes"
	nr_mdns "github.com/dapr/components-contrib/nameresolution/mdns"

	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"

	// Bindings.
	"github.com/dapr/components-contrib/bindings"
	dingtalk_webhook "github.com/dapr/components-contrib/bindings/alicloud/dingtalk/webhook"

	"github.com/dapr/components-contrib/bindings/alicloud/dubbo"
	"github.com/dapr/components-contrib/bindings/alicloud/oss"
	"github.com/dapr/components-contrib/bindings/alicloud/tablestore"
	"github.com/dapr/components-contrib/bindings/apns"
	"github.com/dapr/components-contrib/bindings/aws/dynamodb"
	"github.com/dapr/components-contrib/bindings/aws/kinesis"
	"github.com/dapr/components-contrib/bindings/aws/s3"
	"github.com/dapr/components-contrib/bindings/aws/ses"
	"github.com/dapr/components-contrib/bindings/aws/sns"
	"github.com/dapr/components-contrib/bindings/aws/sqs"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	bindings_cosmosdb "github.com/dapr/components-contrib/bindings/azure/cosmosdb"
	bindings_cosmosdbgremlinapi "github.com/dapr/components-contrib/bindings/azure/cosmosdbgremlinapi"
	"github.com/dapr/components-contrib/bindings/azure/eventgrid"
	"github.com/dapr/components-contrib/bindings/azure/eventhubs"
	"github.com/dapr/components-contrib/bindings/azure/servicebusqueues"
	"github.com/dapr/components-contrib/bindings/azure/signalr"
	"github.com/dapr/components-contrib/bindings/azure/storagequeues"
	"github.com/dapr/components-contrib/bindings/commercetools"
	"github.com/dapr/components-contrib/bindings/cron"
	"github.com/dapr/components-contrib/bindings/gcp/bucket"
	"github.com/dapr/components-contrib/bindings/gcp/pubsub"
	"github.com/dapr/components-contrib/bindings/graphql"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/huawei/obs"
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

	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"

	// HTTP Middleware.

	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/middleware/http/bearer"
	"github.com/dapr/components-contrib/middleware/http/oauth2"
	"github.com/dapr/components-contrib/middleware/http/oauth2clientcredentials"
	"github.com/dapr/components-contrib/middleware/http/opa"
	"github.com/dapr/components-contrib/middleware/http/ratelimit"
	"github.com/dapr/components-contrib/middleware/http/routerchecker"
	"github.com/dapr/components-contrib/middleware/http/sentinel"
	wasm_basic "github.com/dapr/components-contrib/middleware/http/wasm/basic"

	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"

	"github.com/dapr/components-contrib/configuration"
	configuration_azure "github.com/dapr/components-contrib/configuration/azure/appconfig"
	configuration_redis "github.com/dapr/components-contrib/configuration/redis"

	// Lock.
	"github.com/dapr/components-contrib/lock"
	lock_redis "github.com/dapr/components-contrib/lock/redis"

	"github.com/dapr/components-contrib/bindings/alicloud/sls"
)

var (
	log        = logger.NewLogger("dapr.runtime")
	logContrib = logger.NewLogger("dapr.contrib")
)

func main() {
	// set GOMAXPROCS
	_, _ = maxprocs.Set()

	rt, err := runtime.FromFlags()
	if err != nil {
		log.Fatal(err)
	}

	err = rt.Run(
		runtime.WithSecretStores(
			secretstoresLoader.New("kubernetes", func() secretstores.SecretStore {
				return secretstore_kubernetes.NewKubernetesSecretStore(logContrib)
			}),
			secretstoresLoader.New("azure.keyvault", func() secretstores.SecretStore {
				return keyvault.NewAzureKeyvaultSecretStore(logContrib)
			}),
			secretstoresLoader.New("hashicorp.vault", func() secretstores.SecretStore {
				return vault.NewHashiCorpVaultSecretStore(logContrib)
			}),
			secretstoresLoader.New("aws.secretmanager", func() secretstores.SecretStore {
				return secretmanager.NewSecretManager(logContrib)
			}),
			secretstoresLoader.New("aws.parameterstore", func() secretstores.SecretStore {
				return parameterstore.NewParameterStore(logContrib)
			}),
			secretstoresLoader.New("gcp.secretmanager", func() secretstores.SecretStore {
				return gcp_secretmanager.NewSecreteManager(logContrib)
			}),
			secretstoresLoader.New("local.file", func() secretstores.SecretStore {
				return secretstore_file.NewLocalSecretStore(logContrib)
			}),
			secretstoresLoader.New("local.env", func() secretstores.SecretStore {
				return secretstore_env.NewEnvSecretStore(logContrib)
			}),
			secretstoresLoader.New("alicloud.parameterstore", func() secretstores.SecretStore {
				return alicloud_paramstore.NewParameterStore(logContrib)
			}),
			secretstoresLoader.New("huaweicloud.csms", func() secretstores.SecretStore {
				return csms.NewHuaweiCsmsSecretStore(logContrib)
			}),
			secretstoresLoader.New("tencentcloud.ssm", func() secretstores.SecretStore {
				return ssm.NewSSM(logContrib)
			}),
		),
		runtime.WithStates(
			stateLoader.New("redis", func() state.Store {
				return state_redis.NewRedisStateStore(logContrib)
			}),
			stateLoader.New("consul", func() state.Store {
				return consul.NewConsulStateStore(logContrib)
			}),
			stateLoader.New("azure.blobstorage", func() state.Store {
				return state_azure_blobstorage.NewAzureBlobStorageStore(logContrib)
			}),
			stateLoader.New("azure.cosmosdb", func() state.Store {
				return state_cosmosdb.NewCosmosDBStateStore(logContrib)
			}),
			stateLoader.New("azure.tablestorage", func() state.Store {
				return state_azure_tablestorage.NewAzureTablesStateStore(logContrib)
			}),
			stateLoader.New("cassandra", func() state.Store {
				return cassandra.NewCassandraStateStore(logContrib)
			}),
			stateLoader.New("memcached", func() state.Store {
				return memcached.NewMemCacheStateStore(logContrib)
			}),
			stateLoader.New("mongodb", func() state.Store {
				return mongodb.NewMongoDB(logContrib)
			}),
			stateLoader.New("zookeeper", func() state.Store {
				return zookeeper.NewZookeeperStateStore(logContrib)
			}),
			stateLoader.New("gcp.firestore", func() state.Store {
				return firestore.NewFirestoreStateStore(logContrib)
			}),
			stateLoader.New("postgresql", func() state.Store {
				return postgresql.NewPostgreSQLStateStore(logContrib)
			}),
			stateLoader.New("sqlserver", func() state.Store {
				return sqlserver.NewSQLServerStateStore(logContrib)
			}),
			stateLoader.New("hazelcast", func() state.Store {
				return hazelcast.NewHazelcastStore(logContrib)
			}),
			stateLoader.New("couchbase", func() state.Store {
				return couchbase.NewCouchbaseStateStore(logContrib)
			}),
			stateLoader.New("aerospike", func() state.Store {
				return aerospike.NewAerospikeStateStore(logContrib)
			}),
			stateLoader.New("rethinkdb", func() state.Store {
				return rethinkdb.NewRethinkDBStateStore(logContrib)
			}),
			stateLoader.New("aws.dynamodb", state_dynamodb.NewDynamoDBStateStore),
			stateLoader.New("mysql", func() state.Store {
				return state_mysql.NewMySQLStateStore(logContrib)
			}),
			stateLoader.New("oci.objectstorage", func() state.Store {
				return state_oci_objectstorage.NewOCIObjectStorageStore(logContrib)
			}),
			stateLoader.New("jetstream", func() state.Store {
				return state_jetstream.NewJetstreamStateStore(logContrib)
			}),
			stateLoader.New("oracledatabase", func() state.Store {
				return state_oracledatabase.NewOracleDatabaseStateStore(logContrib)
			}),
			stateLoader.New("cockroachdb", func() state.Store {
				return cockroachdb.New(logContrib)
			}),
			stateLoader.New("in-memory", func() state.Store {
				return state_in_memory.NewInMemoryStateStore(logContrib)
			}),
		),
		runtime.WithConfigurations(
			configurationLoader.New("redis", func() configuration.Store {
				return configuration_redis.NewRedisConfigurationStore(logContrib)
			}),
		),
		runtime.WithConfigurations(
			configurationLoader.New("azure.appconfig", func() configuration.Store {
				return configuration_azure.NewAzureAppConfigurationStore(logContrib)
			}),
		),
		runtime.WithLocks(
			lockLoader.New("redis", func() lock.Store {
				return lock_redis.NewStandaloneRedisLock(logContrib)
			}),
		),
		runtime.WithPubSubs(
			pubsubLoader.New("aws.snssqs", func() pubs.PubSub {
				return pubsub_aws_snssqs.NewSnsSqs(logContrib)
			}, "snssqs"), // alias "snssqs" for backwards-compatibility; see dapr/components-contrib#1753
			pubsubLoader.New("azure.eventhubs", func() pubs.PubSub {
				return pubsub_eventhubs.NewAzureEventHubs(logContrib)
			}),
			pubsubLoader.New("azure.servicebus", func() pubs.PubSub {
				return servicebus.NewAzureServiceBus(logContrib)
			}),
			pubsubLoader.New("gcp.pubsub", func() pubs.PubSub {
				return pubsub_gcp.NewGCPPubSub(logContrib)
			}),
			pubsubLoader.New("hazelcast", func() pubs.PubSub {
				return pubsub_hazelcast.NewHazelcastPubSub(logContrib)
			}),
			pubsubLoader.New("jetstream", func() pubs.PubSub {
				return pubsub_jetstream.NewJetStream(logContrib)
			}),
			pubsubLoader.New("kafka", func() pubs.PubSub {
				return pubsub_kafka.NewKafka(logContrib)
			}),
			pubsubLoader.New("mqtt", func() pubs.PubSub {
				return pubsub_mqtt.NewMQTTPubSub(logContrib)
			}),
			pubsubLoader.New("natsstreaming", func() pubs.PubSub {
				return natsstreaming.NewNATSStreamingPubSub(logContrib)
			}),
			pubsubLoader.New("pulsar", func() pubs.PubSub {
				return pubsub_pulsar.NewPulsar(logContrib)
			}),
			pubsubLoader.New("rabbitmq", func() pubs.PubSub {
				return rabbitmq.NewRabbitMQ(logContrib)
			}),
			pubsubLoader.New("rocketmq", func() pubs.PubSub {
				return rocketmq.NewRocketMQ(logContrib)
			}),
			pubsubLoader.New("redis", func() pubs.PubSub {
				return pubsub_redis.NewRedisStreams(logContrib)
			}),
			pubsubLoader.New("in-memory", func() pubs.PubSub {
				return pubsub_inmemory.New(logContrib)
			}),
		),
		runtime.WithNameResolutions(
			nrLoader.New("mdns", func() nr.Resolver {
				return nr_mdns.NewResolver(logContrib)
			}),
			nrLoader.New("kubernetes", func() nr.Resolver {
				return nr_kubernetes.NewResolver(logContrib)
			}),
			nrLoader.New("consul", func() nr.Resolver {
				return nr_consul.NewResolver(logContrib)
			}),
		),
		runtime.WithInputBindings(
			bindingsLoader.NewInput("aws.sqs", func() bindings.InputBinding {
				return sqs.NewAWSSQS(logContrib)
			}),
			bindingsLoader.NewInput("aws.kinesis", func() bindings.InputBinding {
				return kinesis.NewAWSKinesis(logContrib)
			}),
			bindingsLoader.NewInput("azure.eventgrid", func() bindings.InputBinding {
				return eventgrid.NewAzureEventGrid(logContrib)
			}),
			bindingsLoader.NewInput("azure.eventhubs", func() bindings.InputBinding {
				return eventhubs.NewAzureEventHubs(logContrib)
			}),
			bindingsLoader.NewInput("azure.servicebusqueues", func() bindings.InputBinding {
				return servicebusqueues.NewAzureServiceBusQueues(logContrib)
			}),
			bindingsLoader.NewInput("azure.storagequeues", func() bindings.InputBinding {
				return storagequeues.NewAzureStorageQueues(logContrib)
			}),
			bindingsLoader.NewInput("cron", func() bindings.InputBinding {
				return cron.NewCron(logContrib)
			}),
			bindingsLoader.NewInput("dingtalk.webhook", func() bindings.InputBinding {
				return dingtalk_webhook.NewDingTalkWebhook(logContrib)
			}),
			bindingsLoader.NewInput("gcp.pubsub", func() bindings.InputBinding {
				return pubsub.NewGCPPubSub(logContrib)
			}),
			bindingsLoader.NewInput("kafka", func() bindings.InputBinding {
				return kafka.NewKafka(logContrib)
			}),
			bindingsLoader.NewInput("kubernetes", func() bindings.InputBinding {
				return kubernetes.NewKubernetes(logContrib)
			}),
			bindingsLoader.NewInput("mqtt", func() bindings.InputBinding {
				return mqtt.NewMQTT(logContrib)
			}),
			bindingsLoader.NewInput("rabbitmq", func() bindings.InputBinding {
				return bindings_rabbitmq.NewRabbitMQ(logContrib)
			}),
			bindingsLoader.NewInput("rethinkdb.statechange", func() bindings.InputBinding {
				return statechange.NewRethinkDBStateChangeBinding(logContrib)
			}),
			bindingsLoader.NewInput("twitter", func() bindings.InputBinding {
				return twitter.NewTwitter(logContrib)
			}),
			bindingsLoader.NewInput("zeebe.jobworker", func() bindings.InputBinding {
				return bindings_zeebe_jobworker.NewZeebeJobWorker(logContrib)
			}),
		),
		runtime.WithOutputBindings(
			bindingsLoader.NewOutput("alicloud.dubbo", func() bindings.OutputBinding {
				return dubbo.NewDubboOutput(logContrib)
			}),
			bindingsLoader.NewOutput("alicloud.oss", func() bindings.OutputBinding {
				return oss.NewAliCloudOSS(logContrib)
			}),
			bindingsLoader.NewOutput("alicloud.tablestore", func() bindings.OutputBinding {
				return tablestore.NewAliCloudTableStore(log)
			}),
			bindingsLoader.NewOutput("alicloud.sls", func() bindings.OutputBinding {
				return sls.NewAliCloudSlsLogstorage(logContrib)
			}),
			bindingsLoader.NewOutput("apns", func() bindings.OutputBinding {
				return apns.NewAPNS(logContrib)
			}),
			bindingsLoader.NewOutput("aws.s3", func() bindings.OutputBinding {
				return s3.NewAWSS3(logContrib)
			}),
			bindingsLoader.NewOutput("aws.ses", func() bindings.OutputBinding {
				return ses.NewAWSSES(logContrib)
			}),
			bindingsLoader.NewOutput("aws.sqs", func() bindings.OutputBinding {
				return sqs.NewAWSSQS(logContrib)
			}),
			bindingsLoader.NewOutput("aws.sns", func() bindings.OutputBinding {
				return sns.NewAWSSNS(logContrib)
			}),
			bindingsLoader.NewOutput("aws.kinesis", func() bindings.OutputBinding {
				return kinesis.NewAWSKinesis(logContrib)
			}),
			bindingsLoader.NewOutput("aws.dynamodb", func() bindings.OutputBinding {
				return dynamodb.NewDynamoDB(logContrib)
			}),
			bindingsLoader.NewOutput("azure.blobstorage", func() bindings.OutputBinding {
				return blobstorage.NewAzureBlobStorage(logContrib)
			}),
			bindingsLoader.NewOutput("azure.cosmosdb", func() bindings.OutputBinding {
				return bindings_cosmosdb.NewCosmosDB(logContrib)
			}),
			bindingsLoader.NewOutput("azure.cosmosdb.gremlinapi", func() bindings.OutputBinding {
				return bindings_cosmosdbgremlinapi.NewCosmosDBGremlinAPI(logContrib)
			}),
			bindingsLoader.NewOutput("azure.eventgrid", func() bindings.OutputBinding {
				return eventgrid.NewAzureEventGrid(logContrib)
			}),
			bindingsLoader.NewOutput("azure.eventhubs", func() bindings.OutputBinding {
				return eventhubs.NewAzureEventHubs(logContrib)
			}),
			bindingsLoader.NewOutput("azure.servicebusqueues", func() bindings.OutputBinding {
				return servicebusqueues.NewAzureServiceBusQueues(logContrib)
			}),
			bindingsLoader.NewOutput("azure.signalr", func() bindings.OutputBinding {
				return signalr.NewSignalR(logContrib)
			}),
			bindingsLoader.NewOutput("azure.storagequeues", func() bindings.OutputBinding {
				return storagequeues.NewAzureStorageQueues(logContrib)
			}),
			bindingsLoader.NewOutput("cron", func() bindings.OutputBinding {
				return cron.NewCron(logContrib)
			}),
			bindingsLoader.NewOutput("dingtalk.webhook", func() bindings.OutputBinding {
				return dingtalk_webhook.NewDingTalkWebhook(logContrib)
			}),
			bindingsLoader.NewOutput("gcp.bucket", func() bindings.OutputBinding {
				return bucket.NewGCPStorage(logContrib)
			}),
			bindingsLoader.NewOutput("gcp.pubsub", func() bindings.OutputBinding {
				return pubsub.NewGCPPubSub(logContrib)
			}),
			bindingsLoader.NewOutput("http", func() bindings.OutputBinding {
				return http.NewHTTP(logContrib)
			}),
			bindingsLoader.NewOutput("influx", func() bindings.OutputBinding {
				return influx.NewInflux(logContrib)
			}),
			bindingsLoader.NewOutput("kafka", func() bindings.OutputBinding {
				return kafka.NewKafka(logContrib)
			}),
			bindingsLoader.NewOutput("localstorage", func() bindings.OutputBinding {
				return localstorage.NewLocalStorage(logContrib)
			}),
			bindingsLoader.NewOutput("mqtt", func() bindings.OutputBinding {
				return mqtt.NewMQTT(logContrib)
			}),
			bindingsLoader.NewOutput("mysql", func() bindings.OutputBinding {
				return mysql.NewMysql(logContrib)
			}),
			bindingsLoader.NewOutput("postgres", func() bindings.OutputBinding {
				return postgres.NewPostgres(logContrib)
			}),
			bindingsLoader.NewOutput("postmark", func() bindings.OutputBinding {
				return postmark.NewPostmark(logContrib)
			}),
			bindingsLoader.NewOutput("rabbitmq", func() bindings.OutputBinding {
				return bindings_rabbitmq.NewRabbitMQ(logContrib)
			}),
			bindingsLoader.NewOutput("redis", func() bindings.OutputBinding {
				return redis.NewRedis(logContrib)
			}),
			bindingsLoader.NewOutput("smtp", func() bindings.OutputBinding {
				return smtp.NewSMTP(logContrib)
			}),
			bindingsLoader.NewOutput("twilio.sms", func() bindings.OutputBinding {
				return sms.NewSMS(logContrib)
			}),
			bindingsLoader.NewOutput("twilio.sendgrid", func() bindings.OutputBinding {
				return sendgrid.NewSendGrid(logContrib)
			}),
			bindingsLoader.NewOutput("twitter", func() bindings.OutputBinding {
				return twitter.NewTwitter(logContrib)
			}),
			bindingsLoader.NewOutput("zeebe.command", func() bindings.OutputBinding {
				return bindings_zeebe_command.NewZeebeCommand(logContrib)
			}),
			bindingsLoader.NewOutput("graphql", func() bindings.OutputBinding {
				return graphql.NewGraphQL(logContrib)
			}),
			bindingsLoader.NewOutput("huawei.obs", func() bindings.OutputBinding {
				return obs.NewHuaweiOBS(logContrib)
			}),
			bindingsLoader.NewOutput("commercetools", func() bindings.OutputBinding {
				return commercetools.NewCommercetools(logContrib)
			}),
		),
		runtime.WithHTTPMiddleware(
			httpMiddlewareLoader.New("uppercase", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
					return func(ctx *fasthttp.RequestCtx) {
						body := string(ctx.PostBody())
						ctx.Request.SetBody([]byte(strings.ToUpper(body)))
						h(ctx)
					}
				}, nil
			}),
			httpMiddlewareLoader.New("oauth2", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return oauth2.NewOAuth2Middleware().GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("oauth2clientcredentials", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return oauth2clientcredentials.NewOAuth2ClientCredentialsMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("ratelimit", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return ratelimit.NewRateLimitMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("bearer", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return bearer.NewBearerMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("opa", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return opa.NewMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("sentinel", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return sentinel.NewMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("routerchecker", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return routerchecker.NewMiddleware(log).GetHandler(metadata)
			}),
			httpMiddlewareLoader.New("wasm.basic", func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
				return wasm_basic.NewMiddleware(log).GetHandler(metadata)
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
