/*
Package redisconn implements connection to single redis server.

Connection is "wrapper" around a single tcp (unix-socket) connection. All requests are fed into a
single connection, and responses are asynchronously read from it.
Connection is thread-safe, meaning it doesn't need external synchronization.
Connect is responsible for reconnection, but it does not retry requests in the case of networking problems.
*/
package redisconn
