# Change Log

## `v1.3.0`
- add `SystemProperties` to `Event` which contains immutable broker provided metadata (squence number, offset, 
  enqueued time)

## `v1.2.0`
- add websocket support

## `v1.1.5`
- add sender recovery handling for `amqp.ErrLinkClose`, `amqp.ErrConnClosed` and `amqp.ErrSessionClosed`

## `v1.1.4`
- update to amqp 0.11.0 and change sender to use unsettled rather than receiver second mode

## `v1.1.3`
- fix leak in partition persistence 
- fix discarding event properties on batch sending

## `v1.1.2`
- take dep on updated amqp common which has more permissive RPC status description parsing 

## `v1.1.1`
- close sender when hub is closed
- ensure links, session and connections are closed gracefully

## `v1.1.0`
- add receive option to receive from a timestamp
- fix sender recovery on temporary network failures
- add LeasePersistenceInterval to Azure Storage LeaserCheckpointer to allow for customization of persistence interval
  duration

## `v1.0.1`
- fix the breaking change from storage; this is not a breaking change for this library
- move from dep to go modules

## `v1.0.0`
- change from OpenTracing to OpenCensus
- add more documentation for EPH
- variadic mgmt options

## `v0.4.0`
- add partition key to received event [#43](https://github.com/Azure/azure-event-hubs-go/pull/43)
- remove `Receive` in eph in favor of `RegisterHandler`, `UnregisterHandler` and `RegisteredHandlerIDs` [#45](https://github.com/Azure/azure-event-hubs-go/pull/45)

## `v0.3.1`
- simplify environmental construction by preferring SAS

## `v0.3.0`
- pin version of amqp

## `v0.2.1`
- update dependency on common to 0.3.2 to fix retry returning nil error

## `v0.2.0`
- add opentracing support
- add context to close functions (breaking change)

## `v0.1.2`
- remove an extraneous dependency on satori/uuid

## `v0.1.1`
- update common dependency to 0.2.4
- provide more feedback when sending using testhub
- retry send upon server-busy
- use a new connection for each sender and receiver

## `v0.1.0`
- initial release
- basic send and receive
- batched send
- offset persistence
- alpha event host processor with Azure storage persistence
- enabled prefetch batching
