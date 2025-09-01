# Daprd Workflows Reconnect Actor Reuse tests

This set of tests verify the behaviour of the actors based workflows backend when two workers reconnect but they overlap in time.

The overlap in time of two workers means that actors don't get unregistered, and therefore don't get deactivated. This tests verify
that actor state is reused across retries in the execution of workflows, and the reuse of that state does not cause unexpected behaviors.
