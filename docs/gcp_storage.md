# GCP Storage Bucket Event Source

This tutorial shows you how to setup a GCP Storage Bucket event source to read or write data to/from.

## Prerequisites

1. An GCP account with a provisioned Storage bucket. [(instructions)](https://cloud.google.com/storage/docs/creating-buckets).
2. A service account key JSON file. [(instructions)](https://cloud.google.com/iam/docs/creating-managing-service-account-keys).

## Create the configuration file

Create a file called gcp_storage.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: <NAME>
spec:
  type: gcp.storage.bucket
  connectionInfo:
    bucket: <BUCKET-NAME>
    type: <TYPE>
    project_id: <PROJECT-ID>
    private_key_id: <PRIVATE-KEY-ID>
    private_key: <PRIVATE-KEY>
    client_email: <CLIENT-EMAIL>
    client_id: <CLIENT-ID>
    auth_uri: <AUTH-URI>
    token_uri: <TOKEN-URI>
    auth_provider_x509_cert_url: <AUTH-PROVIDER-CERT-URL>
    client_x509_cert_url: <CLIENT-CERT-URL>
```

Be sure to fill in the correct values for ```connectionInfo``` from the service key you created with the ```gcloud``` tool.
Make sure to give your EventSource a ```name``` - this is later put in your app code in order to receive events from this source.

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./gcp_storage.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy gcp_storage.yaml to that directory.

```
mkdir -p eventsources
cp gcp_storage.yaml ./eventsources
```