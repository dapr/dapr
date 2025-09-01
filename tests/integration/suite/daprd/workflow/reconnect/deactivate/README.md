# Daprd Workflows Reconnect Actor Deactivation tests

This set of tests verify the behaviour of the actors based workflows backend when an actor is deactivated midway executing a workflow.

To achieve that the tests stop the workflows worker, while executing a workflow, and then reconnect a new worker.