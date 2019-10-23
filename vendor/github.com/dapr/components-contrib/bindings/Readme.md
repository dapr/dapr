# Bindings

Bindings provide a common way to trigger an application with events from external systems, or invoke an external system with optional data payloads.
Bindings are great for event-driven, on-demand compute and help reduce boilerplate code.

List of bindings and their status:

| Name  | Input Binding | Output Binding | Status
| ------------- | -------------- | -------------  | ------------- |
| [Kafka](./kafka) | V | V | Experimental |
| [RabbitMQ](./rabbitmq) | V  | V | Experimental |
| [AWS SQS](./sqs) | V | V | Experimental |
| [AWS SNS](./sns) |  | V | Experimental |
| [GCP Cloud Pub/Sub](./pubsub) | V | V | Experimental |
| [Azure EventHubs](./eventhubs) | V | V | Experimental |
| [Azure CosmosDB](./cosmosdb) | | V | Experimental |
| [GCP Storage Bucket](./gcpbucket)  | | V | Experimental |
| [HTTP](./http) |  | V | Experimental |
| [MQTT](./mqtt) | V | V | Experimental |
| [Redis](./redis) |  | V | Experimental |
| [AWS DynamoDB](./dynamodb) | | V | Experimental |
| [AWS S3](./s3) | | V | Experimental |
| [Azure Blob Storage](./blobstorage) | | V | Experimental |
| [Azure Service Bus Queues](./servicebusqueues) | V | V | Experimental |
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
