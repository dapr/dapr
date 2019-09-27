# Bindings

Bindings provide a common way to trigger an application with events from external systems, or invoke an external system with optional data payloads.
Bindings are great for event-driven, on-demand compute and help reduce boilerplate code.

List of bindings and their status:

| Name  | Input Binding | Output Binding | Status
| ------------- | -------------- | -------------  | ------------- |
| Kafka | V | V | Experimental |
| RabbitMQ | V  | V | Experimental |
| AWS SQS | V | V | Experimental |
| AWS SNS |  | V | Experimental |
| Azure EventHubs | V | V | Experimental |
| Azure CosmosDB | | V | Experimental |
| GCP Storage Bucket  | | V | Experimental |
| HTTP |  | V | Experimental |
| MQTT | V | V | Experimental |
| Redis |  | V | Experimental |
| AWS DynamoDB | | V | Experimental |
| AWS S3 | | V | Experimental |
| Azure Blob Storage | | V | Experimental |
| Azure Service Bus Queues | V | V | Experimental |

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
