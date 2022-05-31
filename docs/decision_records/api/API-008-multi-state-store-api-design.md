# API-008: Multi State store API design

## Status
Accepted

## Context
This decision record is to support multiple state stores support in Dapr. We agreed on the decision to introduce the breaking change in API
to support multi state store with no backward compatibility.

With this change , the state API allows the app to target a specific state store by store-name, for example:

v1.0/state/storeA/

v1.0/state/storeB/

Earlier this breaking change, the API is v1.0/state/`<key>`

We have reviewed multi storage API design for completeness and consistency.

## Decisions

*  New state store API is v1.0/state/`<store-name>`/
*  If user is using actors and like to persist the state then user must provide actorStateStore: true in the configuration yaml.

   If the attribute is not specified or multiple actor state stores are configured, Dapr runtime will log warning.
   
   The actor API to save the state will fail in both these scenarios where actorStore is not specified or multiple actor stores
are specified.
*  It is noted that after this breaking change, actor state store has to be specified unlike earlier where first state store is picked up by default.
* It is noted that this breaking change will also require a CLI change to generate the state store YAML for redis with actorStateStore.

* To provide multiple stores, user has to provide separate YAML for each store and giving unique name for the store.
* It is noted that the param's keyPrefix represents state key prefix, it's value included ${appid} is the microservice appid, ${name} is the CRDs component's unique name, ${none} is non key prefix and the custom key prefix

  For example, below are the 2 sample yaml files in which redis store is used as actor state store while mongodb store is not used as actor state store.

```
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: myStore1  # Required. This is the unique name of the store.
spec:
  type: state.redis
  metadata:
  - name: keyPrefix
    value: none # Optional. default appid. such as: appid, none, name and custom key prefix
  - name: <KEY>
    value: <VALUE>
  - name: <KEY>
    value: <VALUE>
  - name: actorStateStore  # Optional. default: false
    value : true
```

```
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: myStore2 # Required. This is the unique name of the store.
spec:
  type: state.mongodb
  metadata:
  - name: keyPrefix
    value: none # Optional. default appid. such as: appid, none, name and custom key prefix
  - name: <KEY>
    value: <VALUE>
  - name: <KEY>
    value: <VALUE>

```

So with the above example, the state APIs will be : v1.0/state/myStore1/`<key>`
and v1.0/state/myStore2/`<key>`
## Consequences

With these changes we should meet multiple state stores requirements.
