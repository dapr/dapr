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
*  User has to provide actorStateStore: true to specify the actor state store in the configuration yaml. If the attribute is not specified, Dapr runtime will log warning.
   Also if multiple actor state stores are configured, Dapr runtime will log warning.
*  It is noted that after this breaking change, actor state store has to be specifed unlike earlier where first state store is picked up by default.
* It is noted that this breaking change will also require a CLI change to generate the state store YAML for redis with actorStateStore.

* To provide multiple stores, user has to provide separate YAML for each store and giving unique name for the store.

  A state store in Dapr is described using a `Component` file and **statestorename** is the name of the store.

```
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestorename
spec:
  type: state.<DATABASE>
  metadata:
  - name: <KEY>
    value: <VALUE>
  - name: <KEY>
    value: <VALUE>
...
```

So with the above example, the state API will be : v1.0/state/statestorename/`<key>`
## Consequences

With these changes we should meet multiple state stores requirements.