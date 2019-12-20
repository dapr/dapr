# API-006: Universal Namespace

## Status 
Proposed

## Context
For cloud-edge hybrid scenarios and multie-region deployment scenarios, we need the ability to facilitate communications cross clusters. Specifically, it's desirable to have services scoped by cluster names so that a service in one cluster can address and invoke services on another trusted cluster through fully qualified names in a universal namespace, such as cluster1.serviceb. 

## Decisions
We should consider adding universal namespace capabilities to Dapr.