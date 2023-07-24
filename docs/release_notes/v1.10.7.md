# Dapr 1.10.7

## Fixed an issue in Kafka components with SASL authentication

### Problem

When attempting to connect to Kafka compatible services like Azure Event Hubs using SALS authentication the connection fails with the message "client has run out of available brokers to talk to."

### Impact

The issue impacts users on Dapr 1.10.x who use Kafka components to connect to Azure Event Hubs. Other Kafka compatible services may be impacted too.

### Root cause

The latest versions (1.38.X) of the shopify/sarama kafka library introduced this problem.

### Solution

Downgrading the shopify/sarama library to release 1.37.2 resolves this problem. An issue with the maintainers of the sarama library has been created for further investigation.

## Fixed an issue with HTTP URL routing in Dapr sidecar

### Problem

When using the Dapr sidecar HTTP APIs, some HTTP URLs where not being routed to
the correct handler. For example, when prefixing the URL path with duplicated
slashes. i.e. `//v1.0/actors/DemoActor/1234/method/SetReminder`.

### Impact

Since v1.10.0, requests which did not have perfect paths according to the API
may be routed to the wrong API handler, and typically return some kind of error.

### Root cause

The HTTP router inside Dapr was normalizing the request's HTTP path only after
evaluating the route, causing routing errors with un-normalized paths.

### Solution

We changed the router used by the Dapr HTTP server to normalize HTTP paths
before matching the route.
