# API-004: Bindg Manifests

## Status
Accepted

## Context
As we rename Event Sources to Bindings, and formally separate State Stores, Message Buses, and Bindings, we need to decide if we need to introduce different manifest types.

## Decisions

### Dapr 

* All components use the same **Component** manifests, identified by a component **type**.
* We'll come up with a mechanism to support pluggable secret stores. We'll support Kubernetes native secret store and Azure Key Vault in the initial release.