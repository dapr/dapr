// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

import (
	"github.com/dapr/components-contrib/bindings/blobstorage"
	"github.com/dapr/components-contrib/bindings/cosmosdb"
	"github.com/dapr/components-contrib/bindings/dynamodb"
	"github.com/dapr/components-contrib/bindings/eventhubs"
	"github.com/dapr/components-contrib/bindings/gcpbucket"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/kafka"
	"github.com/dapr/components-contrib/bindings/kubernetes"
	"github.com/dapr/components-contrib/bindings/mqtt"
	"github.com/dapr/components-contrib/bindings/pubsub"
	"github.com/dapr/components-contrib/bindings/rabbitmq"
	"github.com/dapr/components-contrib/bindings/redis"
	"github.com/dapr/components-contrib/bindings/s3"
	"github.com/dapr/components-contrib/bindings/servicebusqueues"
	"github.com/dapr/components-contrib/bindings/sns"
	"github.com/dapr/components-contrib/bindings/sqs"
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
	RegisterInputBinding("kubernetes", kubernetes.NewKubernetes())
}
