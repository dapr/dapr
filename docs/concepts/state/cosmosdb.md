# Actions State Management using CosmosDB

Actions supports using CosmosDB as a state store, for cases where 99.999% SLA or global replication are needed.

## Prerequisites

* A CosmosDB account, database and collection. See [here](https://docs.microsoft.com/en-us/azure/cosmos-db/sql-api-get-started) for instructions on how to get started with CosmosDB.

## Create the configuration file

Create a file called cosmos.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: statestore
spec:
  type: actions.state.cosmosdb
  connectionInfo:
    url: "<COSMOS-URL>"
    masterKey: "<COSMOS-PRIMARY-KEY>"
    database: "<COSMOS-DATABASE-NAME>"
    collection: "<COSMOS-COLLECTION-NAME>"
```

Be sure to fill in the correct values for ```url```, ```masterKey```, ```database``` and ```collection```.
That's it, you're done!

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./cosmos.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy cosmos.yaml to that directory.

```
mkdir -p eventsources
cp cosmos.yaml ./eventsources
```