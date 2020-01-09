// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

import (
	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/aws/dynamodb"
	"github.com/dapr/components-contrib/bindings/aws/s3"
	"github.com/dapr/components-contrib/bindings/aws/sns"
	"github.com/dapr/components-contrib/bindings/aws/sqs"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	"github.com/dapr/components-contrib/bindings/azure/cosmosdb"
	"github.com/dapr/components-contrib/bindings/azure/eventhubs"
	"github.com/dapr/components-contrib/bindings/azure/servicebusqueues"
	"github.com/dapr/components-contrib/bindings/azure/signalr"
	"github.com/dapr/components-contrib/bindings/gcp/bucket"
	"github.com/dapr/components-contrib/bindings/gcp/pubsub"
	"github.com/dapr/components-contrib/bindings/http"
	"github.com/dapr/components-contrib/bindings/kafka"
	"github.com/dapr/components-contrib/bindings/kubernetes"
	"github.com/dapr/components-contrib/bindings/mqtt"
	"github.com/dapr/components-contrib/bindings/rabbitmq"
	"github.com/dapr/components-contrib/bindings/redis"
)

// Load input/output bindings
func Load() {
	RegisterInputBinding("aws.sqs", func() bindings.InputBinding {
		return sqs.NewAWSSQS()
	})
	RegisterOutputBinding("aws.sqs", func() bindings.OutputBinding {
		return sqs.NewAWSSQS()
	})
	RegisterOutputBinding("aws.sns", func() bindings.OutputBinding {
		return sns.NewAWSSNS()
	})
	RegisterInputBinding("azure.eventhubs", func() bindings.InputBinding {
		return eventhubs.NewAzureEventHubs()
	})
	RegisterOutputBinding("azure.eventhubs", func() bindings.OutputBinding {
		return eventhubs.NewAzureEventHubs()
	})
	RegisterOutputBinding("aws.dynamodb", func() bindings.OutputBinding {
		return dynamodb.NewDynamoDB()
	})
	RegisterOutputBinding("azure.cosmosdb", func() bindings.OutputBinding {
		return cosmosdb.NewCosmosDB()
	})
	RegisterOutputBinding("gcp.bucket", func() bindings.OutputBinding {
		return bucket.NewGCPStorage()
	})
	RegisterOutputBinding("http", func() bindings.OutputBinding {
		return http.NewHTTP()
	})
	RegisterInputBinding("kafka", func() bindings.InputBinding {
		return kafka.NewKafka()
	})
	RegisterOutputBinding("kafka", func() bindings.OutputBinding {
		return kafka.NewKafka()
	})
	RegisterInputBinding("mqtt", func() bindings.InputBinding {
		return mqtt.NewMQTT()
	})
	RegisterOutputBinding("mqtt", func() bindings.OutputBinding {
		return mqtt.NewMQTT()
	})
	RegisterInputBinding("rabbitmq", func() bindings.InputBinding {
		return rabbitmq.NewRabbitMQ()
	})
	RegisterOutputBinding("rabbitmq", func() bindings.OutputBinding {
		return rabbitmq.NewRabbitMQ()
	})
	RegisterOutputBinding("redis", func() bindings.OutputBinding {
		return redis.NewRedis()
	})
	RegisterOutputBinding("aws.s3", func() bindings.OutputBinding {
		return s3.NewAWSS3()
	})
	RegisterOutputBinding("azure.blobstorage", func() bindings.OutputBinding {
		return blobstorage.NewAzureBlobStorage()
	})
	RegisterInputBinding("azure.servicebusqueues", func() bindings.InputBinding {
		return servicebusqueues.NewAzureServiceBusQueues()
	})
	RegisterOutputBinding("azure.servicebusqueues", func() bindings.OutputBinding {
		return servicebusqueues.NewAzureServiceBusQueues()
	})
	RegisterInputBinding("gcp.pubsub", func() bindings.InputBinding {
		return pubsub.NewGCPPubSub()
	})
	RegisterOutputBinding("gcp.pubsub", func() bindings.OutputBinding {
		return pubsub.NewGCPPubSub()
	})
	RegisterInputBinding("kubernetes", kubernetes.NewKubernetes)
	RegisterOutputBinding("azure.signalr", func() bindings.OutputBinding {
		return signalr.NewSignalR()
	})
}
