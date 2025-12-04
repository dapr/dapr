# Dapr 1.16.3

This update includes bug fixes:

- [Sftp binding not handling reconnections](#sftp-binding-not-handling-reconnections)

## Sftp binding not handling reconnections

### Problem

The SFTP binding, introduced in **v1.15.0**, did not correctly handle reconnections.
If the SFTP connection was closed externally (outside the Dapr sidecar), the sidecar would not attempt to reconnect.

### Impact

In scenarios where the SFTP server or network closed the connection, the Dapr sidecar lost connectivity permanently and required a restart to restore SFTP communication.

### Root Cause

The SFTP binding maintained a single long-lived connection and did not attempt to recreate it when operations failed due to network or server-side disconnects.
Once the underlying SFTP/SSH session was closed, subsequent binding operations continued to use the stale connection instead of establishing a new one, leaving the binding in a permanently broken state until the sidecar was restarted.

### Solution

A new reconnection mechanism was added to the SFTP binding ([PR](https://github.com/dapr/components-contrib/pull/4098)).
When an SFTP action fails due to a connection issue, the binding now attempts to reconnect to the server and restore connectivity automatically, avoiding the need to restart the sidecar.
