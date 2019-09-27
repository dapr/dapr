# Change Log

## `head`

## `v0.9.1`
- bump common version to v2.1.0

## `v0.9.0`
- periodically refresh claims based auth for connections to resolve [issue #116](https://github.com/Azure/azure-service-bus-go/issues/116)
- refactor management functionality for entities into composition structs
- fix session deferral for queues and subscriptions
- add topic scheduled messages

## `v0.8.0`
- tab for tracing and logging which supports both opencensus and opentracing. To use opencensus, just add a
  `_ "github.com/devigned/tab/opencensus"`. To use opentracing, just add a `_ "github.com/devigned/tab/opentracing"`
- target azure-amqp-common-go/v2

## `v0.7.0`
- [add batch disposition errors](https://github.com/Azure/azure-service-bus-go/pull/129)

## `v0.6.0`
- add namespace TLS configuration option
- update to Azure SDK v28 and AutoRest 12

## `v0.5.1`
- update the Azure Resource Manager dependency to the latest to help protect people not using a dependency 
  management tool such as `dep` or `vgo`.

## `v0.5.0`
- add support for websockets

## `v0.4.1`
- fix issue with sender when SB returns a different receiver disposition [#119](https://github.com/Azure/azure-service-bus-go/issues/119)

## `v0.4.0`
- Update to AMQP 0.11.0 which introduces strict settlement mode
  ([#111](https://github.com/Azure/azure-service-bus-go/issues/111))

## `v0.3.0`
- Add disposition batching
- Add NotFound errors for mgmt API
- Fix go routine leak when listening for messages upon context close
- Add batch sends for Topics

## `v0.2.0`
- Refactor disposition handler so that errors can be handled in handlers
- Add dead letter queues for entities
- Fix connection leaks when using multiple calls to Receive
- Ensure senders wait for message disposition before returning

## `v0.1.0`
- initial tag for Service Bus which includes Queues, Topics and Subscriptions using AMQP