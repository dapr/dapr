# Actions State Management using Redis

Redis is used by Actions for two purposes:

1. Message delivery between Actions in an async manner
2. State persistence and restoration

Any Redis instance is welcome - containerized, running on your local dev machine, AWS ElasticCache, Azure RedisCache, etc.

## Create the configuration file

Create a file called redis.yaml, and paste the following:

```
apiVersion: actions.io/v1alpha1
kind: EventSource
metadata:
  name: statestore
spec:
  type: actions.state.redis
  connectionInfo:
    redisHost: "<HOST>:<PORT>"
    redisPassword: "<PASSWORD>"
```

Be sure to fill in the correct values for ```redisHost``` and ```redisPassword```.
That's it, you're done!

## Apply the configuration

### If running in Kubernetes

```
kubectl apply -f ./redis.yaml
```

### If running as standalone

Create a directory named ```eventsources``` in the root path of your Action binary.
Copy redis.yaml to that directory.

```
mkdir -p eventsources
cp redis.yaml ./eventsources
```