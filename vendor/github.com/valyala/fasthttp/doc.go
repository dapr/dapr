/*
Package fasthttp provides fast HTTP server and client API.

Fasthttp provides the following features:

    * Optimized for speed. Easily handles more than 100K qps and more than 1M
      concurrent keep-alive connections on modern hardware.
    * Optimized for low memory usage.
    * Easy 'Connection: Upgrade' support via RequestCtx.Hijack.
    * Server supports requests' pipelining. Multiple requests may be read from
      a single network packet and multiple responses may be sent in a single
      network packet. This may be useful for highly loaded REST services.
    * Server provides the following anti-DoS limits:

        * The number of concurrent connections.
        * The number of concurrent connections per client IP.
        * The number of requests per connection.
        * Request read timeout.
        * Response write timeout.
        * Maximum request header size.
        * Maximum request body size.
        * Maximum request execution time.
        * Maximum keep-alive connection lifetime.
        * Early filtering out non-GET requests.

    * A lot of additional useful info is exposed to request handler:

        * Server and client address.
        * Per-request logger.
        * Unique request id.
        * Request start time.
        * Connection start time.
        * Request sequence number for the current connection.

    * Client supports automatic retry on idempotent requests' failure.
    * Fasthttp API is designed with the ability to extend existing client
      and server implementations or to write custom client and server
      implementations from scratch.
*/
package fasthttp
