# Secrets

Components can reference secrets for the `spec.metadata` section.<br>
In order to reference a secret, you need to set the `auth.secretStore` field to specify the name of the secret store that holds the secrets.<br><br>
When running in Kubernetes, if the `auth.secretStore` is empty, the Kubernetes secret store is assumed.

## Examples

Using plain text:

```
apiVersion: actions.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: MyPassword
```

Using a Kubernetes secret:

```
apiVersion: actions.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    secretKeyRef:
    	name: redis-secret
        key:  redis-password
auth:
  secretStore: kubernetes
```

The above example tells Actions to use the `kubernetes` secret store, extract a secret named `redis-secret` and assign the value of the `redis-password` key in the secret to the `redisPassword` field in the Component.

### Creating a secret and referencing it in a Component

The following example shows you how to create a Kubernetes secret to hold the connection string for an Event Hubs binding.

First, create the Kubernetes secret:

```
kubectl create secret generic eventhubs-secret --from-literal=connectionString=*********
```

Next, reference the secret in your binding:

```
apiVersion: actions.io/v1alpha1
kind: Component
metadata:
  name: eventhubs
spec:
  type: bindings.azure.eventhubs
  metadata:
  - name: connectionString
    secretKeyRef:
      name: eventhubs-secret
      key: connectionString
```

Finally, apply the component to the Kubernetes cluster:

```
kubectl apply -f ./eventhubs.yaml
```

All done!