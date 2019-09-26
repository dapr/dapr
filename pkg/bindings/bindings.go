package bindings

import (
	"github.com/actionscore/actions/pkg/bindings/cosmosdb"
	"github.com/actionscore/actions/pkg/bindings/dynamodb"
	"github.com/actionscore/actions/pkg/bindings/eventhubs"
	"github.com/actionscore/actions/pkg/bindings/gcpbucket"
	"github.com/actionscore/actions/pkg/bindings/http"
	"github.com/actionscore/actions/pkg/bindings/kafka"
	"github.com/actionscore/actions/pkg/bindings/mqtt"
	"github.com/actionscore/actions/pkg/bindings/rabbitmq"
	"github.com/actionscore/actions/pkg/bindings/redis"
	"github.com/actionscore/actions/pkg/bindings/sns"
	"github.com/actionscore/actions/pkg/bindings/sqs"
	"github.com/actionscore/actions/pkg/components/bindings"
)

// Load input/output bindings
func Load() {
	bindings.RegisterInputBinding("aws.sqs", sqs.NewAWSSQS())
	bindings.RegisterOutputBinding("aws.sqs", sqs.NewAWSSQS())
	bindings.RegisterOutputBinding("aws.sns", sns.NewAWSSNS())
	bindings.RegisterInputBinding("azure.eventhubs", eventhubs.NewAzureEventHubs())
	bindings.RegisterOutputBinding("azure.eventhubs", eventhubs.NewAzureEventHubs())
	bindings.RegisterOutputBinding("aws.dynamodb", dynamodb.NewDynamoDB())
	bindings.RegisterOutputBinding("azure.cosmosdb", cosmosdb.NewCosmosDB())
	bindings.RegisterOutputBinding("gcp.bucket", gcpbucket.NewGCPStorage())
	bindings.RegisterInputBinding("http", http.NewHTTP())
	bindings.RegisterOutputBinding("http", http.NewHTTP())
	bindings.RegisterInputBinding("kafka", kafka.NewKafka())
	bindings.RegisterOutputBinding("kafka", kafka.NewKafka())
	bindings.RegisterInputBinding("mqtt", mqtt.NewMQTT())
	bindings.RegisterOutputBinding("mqtt", mqtt.NewMQTT())
	bindings.RegisterInputBinding("rabbitmq", rabbitmq.NewRabbitMQ())
	bindings.RegisterOutputBinding("rabbitmq", rabbitmq.NewRabbitMQ())
	bindings.RegisterOutputBinding("redis", redis.NewRedis())
}
