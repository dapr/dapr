# Distributed Tracing

Actions uses [OpenCensus](https://opencensus.io/) for distributed tracing. This tutorial shows you how to configure distributed tracing to export call traces to [Zipkin](https://zipkin.io/). For more details on configuring exporters to other tracing backends such as [Azure Monitor](https://azure.microsoft.com/en-us/services/monitor/), [Prometheus](https://prometheus.io/) and [AWS X-Ray](https://aws.amazon.com/xray/), please see OpenCensus's [exporters page](https://opencensus.io/exporters/).

## Prerequisites
1. Java runtime (for running Zipkin)
2. Latest Action runtime binary 
3. Go (for running the test app)
## Set up Zipkin
1. Download Zipkin:
```bash
curl -sSL https://zipkin.io/quickstart.sh | bash -s
```
2. Start Zipkin
```bash
java -jar zipkin.jar
```
3. Verify Zipkin is working by visiting its Web UI at: http://localhost:9411

## Create a test app
1. Create a new Go app named test.ao with the following code:

```go
package main

import (
  mocks "github.com/actionscore/actions/pkg/testing"
)

func main() {
  app := mocks.NewMockApp(false, 10000, false)
  app.Run(8080)
}

```

2. Launch the test app:

```bash
go run test.go
```

## Create the configuration file
To configure distributed tracing for Actions, you need to create an Actions configuration file, which contains the tracing specification. Create a file named actions_config.yaml with the following content:

```yaml
spec:
  tracing:
    enabled: true
    exporterType: zipkin
    exporterAddress: "http://localhost:9411/api/v2/spans"
    expandParams: true
    includeBody: true
```
## Launch Actions runtime with the configuration file

The following command launches Actions runtime with the configuration file:
```bash
./actionsrt.exe -config ./actions_config.yaml -actions-http-port 3510 -app-port 8080 -actions-grpc-port 50000 -actions-id action-1
```

## Send a test request
Send a POST request to http://localhost:3510/v1.0/actions/action-1/httptest with the following JSON payload:

```json
{
	"data": {
		"angle": 315
	}
}
```
## Observe the traces

Navigate to the Zipkin UI and observe the traces:

![sample trace](./imgs/sample_trace.png) 