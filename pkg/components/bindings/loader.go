package bindings

import (
	"github.com/actionscore/components-contrib/bindings/blobstorage"
	"github.com/actionscore/components-contrib/bindings/cosmosdb"
	"github.com/actionscore/components-contrib/bindings/dynamodb"
	"github.com/actionscore/components-contrib/bindings/eventhubs"
	"github.com/actionscore/components-contrib/bindings/gcpbucket"
	"github.com/actionscore/components-contrib/bindings/http"
	"github.com/actionscore/components-contrib/bindings/kafka"
	"github.com/actionscore/components-contrib/bindings/mqtt"
	"github.com/actionscore/components-contrib/bindings/pubsub"
	"github.com/actionscore/components-contrib/bindings/rabbitmq"
	"github.com/actionscore/components-contrib/bindings/redis"
	"github.com/actionscore/components-contrib/bindings/s3"
	"github.com/actionscore/components-contrib/bindings/servicebusqueues"
	"github.com/actionscore/components-contrib/bindings/sns"
	"github.com/actionscore/components-contrib/bindings/sqs"
)

// Load input/output bindings
func Load() {
	RegisterInputBinding("aws.sqs", sqs.NewAWSSQS())
	RegisterOutputBinding("aws.sqs", sqs.NewAWSSQS())
	RegisterOutputBinding("aws.sns", sns.NewAWSSNS())
	RegisterInputBinding("azure.eventhubs", eventhubs.NewAzureEventHubs())
	RegisterOutputBinding("azure.eventhubs", eventhubs.NewAzureEventHubs())
	RegisterOutputBinding("aws.dynamodb", dynamodb.NewDynamoDB())
	RegisterOutputBinding("azure.cosmosdb", cosmosdb.NewCosmosDB())
	RegisterOutputBinding("gcp.bucket", gcpbucket.NewGCPStorage())
	RegisterInputBinding("http", http.NewHTTP())
	RegisterOutputBinding("http", http.NewHTTP())
	RegisterInputBinding("kafka", kafka.NewKafka())
	RegisterOutputBinding("kafka", kafka.NewKafka())
	RegisterInputBinding("mqtt", mqtt.NewMQTT())
	RegisterOutputBinding("mqtt", mqtt.NewMQTT())
	RegisterInputBinding("rabbitmq", rabbitmq.NewRabbitMQ())
	RegisterOutputBinding("rabbitmq", rabbitmq.NewRabbitMQ())
	RegisterOutputBinding("redis", redis.NewRedis())
	RegisterOutputBinding("aws.s3", s3.NewAWSS3())
	RegisterOutputBinding("azure.blobstorage", blobstorage.NewAzureBlobStorage())
	RegisterInputBinding("azure.servicebusqueues", servicebusqueues.NewAzureServiceBusQueues())
	RegisterOutputBinding("azure.servicebusqueues", servicebusqueues.NewAzureServiceBusQueues())
	RegisterInputBinding("gcp.pubsub", pubsub.NewGCPPubSub())
	RegisterOutputBinding("gcp.pubsub", pubsub.NewGCPPubSub())
}
