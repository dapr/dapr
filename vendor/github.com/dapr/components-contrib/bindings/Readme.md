# Bindings

Bindings provide a common way to trigger an application with events from external systems, or invoke an external system with optional data payloads.
Bindings are great for event-driven, on-demand compute and help reduce boilerplate code.

List of bindings and their status:

| Name  | Input Binding | Output Binding | Status
| ------------- | -------------- | -------------  | ------------- |
| [Kafka](./kafka) | V | V | Experimental |
| [RabbitMQ](./rabbitmq) | V  | V | Experimental |
| [AWS SQS](./aws/sqs) | V | V | Experimental |
| [AWS SNS](./aws/sns) |  | V | Experimental |
| [GCP Cloud Pub/Sub](./gcp/pubsub) | V | V | Experimental |
| [Azure EventHubs](./azure/eventhubs) | V | V | Experimental |
| [Azure CosmosDB](./azure/cosmosdb) | | V | Experimental |
| [GCP Storage Bucket](./gcp/bucket)  | | V | Experimental |
| [HTTP](./http) |  | V | Experimental |
| [MQTT](./mqtt) | V | V | Experimental |
| [Redis](./redis) |  | V | Experimental |
| [AWS DynamoDB](./aws/dynamodb) | | V | Experimental |
| [AWS S3](./aws/s3) | | V | Experimental |
| [Azure Blob Storage](./azure/blobstorage) | | V | Experimental |
| [Azure Service Bus Queues](./azure/servicebusqueues) | V | V | Experimental |
| [Kubernetes Events](./kubernetes) | V | | Experimental |

## Implementing a new binding

A compliant binding needs to implement one or more interfaces, depending on the type of binding (Input or Output):

Input binding:

```
type InputBinding interface {
	Init(metadata Metadata) error
	Read(handler func(*ReadResponse) error) error
}
```

Output binding:

```
type OutputBinding interface {
	Init(metadata Metadata) error
	Write(req *WriteRequest) error
}
```
