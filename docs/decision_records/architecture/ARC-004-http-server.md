# ARC-004: HTTP API server

## Status

Proposed

## Context

Go community has the multiple http server implementations, such as go net/http, fasthttp, gin, to serve HTTP Restful API. This decision records describes which http server implementation uses in Dapr.

## Decisions

* Use [fasthttp server](https://github.com/valyala/fasthttp) implementation because fasthttp offers [the best performance and lowest resource usages](https://github.com/valyala/fasthttp#http-server-performance-comparison-with-nethttp) for the existing HTTP 1.1 server
* Use [fasthttpadaptor](https://godoc.org/github.com/valyala/fasthttp/fasthttpadaptor) if you need to convert fasthttp request context to net/http context.

## Consequences

Using FASTHTTP, Dapr can handle high throughput HTTP requests with the lowest cpu/mem resources.
