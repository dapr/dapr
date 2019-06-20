# AWS SQS Event Source

This tutorial shows you how to setup an AWS SQS event source to read or write data to/from.

## Prerequisites

1. An AWS account with a provisioned SQS instance [(instructions)](https://aws.amazon.com/sqs/getting-started/).

## Create the configuration file

Create a file called aws_sqs.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: <NAME>
spec:
  type: aws.messaging.sqs
  connectionInfo:
    queueName: "<QUEUE-NAME>"
    region: "<AWS-REGION>"
    accessKey: "<ACCESS-KEY>"
    secretKey: "<SECRET-KEY>"
```

Be sure to fill in the correct values for ```connectionInfo``` from your AWS console.
Make sure to give your EventSource a ```name``` - this is later put in your app code in order to receive events from this source.

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./aws_sqs.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy aws_sqs.yaml to that directory.

```
mkdir -p eventsources
cp aws_sqs.yaml ./eventsources
```