# Azure Event Hubs Event Source

This tutorial shows you how to setup an Azure Event Hubs event source to read or write data to/from.

## Prerequisites

1. An Azure account with a provisioned Event Hubs instance [(instructions)](https://docs.microsoft.com/en-us/azure/event-hubs/).

## Create the configuration file

Create a file called azure_eventhubs.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: <NAME>
spec:
  type: azure.messaging.eventhubs
  connectionInfo:
    connectionString: <EVENTHUBS-CONNECTION-STRING>
```

Be sure to fill in the correct values for ```connectionInfo``` from your Azure account.
Make sure to give your EventSource a ```name``` - this is later put in your app code in order to receive events from this source.

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./azure_eventhubs.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy azure_eventhubs.yaml to that directory.

```
mkdir -p eventsources
cp azure_eventhubs.yaml ./eventsources
```