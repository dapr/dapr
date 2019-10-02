# API-003: Messaging API names

## Status
Accepted

## Context
Our existing messaging interface names lack of clarity. This review was to make sure messaging interfaces were named appropriately to avoid possible confusions.

## Decisions

### Dapr 

* All messaging APIs are grouped under a **messaging** namespace/package.
* We define three distinct messaging interfaces:
    - **direct**
      One-to-one messaging between two parties: a sender sending message to a recipient.
    - **broadcast**
      One-to-many messaging: a sender sending message to a list of recipients. 
    - **pub-sub**
      Messaging through pub-sub: a publisher publishing to a topic, to which subscribers subscribe.
* We distinguish message and direct invocation. For messaging, we guarantee at-least-once delivery. For direct invocation, we provide best-attempt delivery.
## Consequences

We should achieve better clarity on messaging behaviors.