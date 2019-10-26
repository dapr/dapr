# State Stores

State Stores provide a common way to interact with different data store implementations, and allow users to opt-in to advanced capabilities using defined metadata.

Currently supported state stores are:

* Redis
* HashiCorp Consul
* Etcd
* Cassandra
* Azure CosmosDB

## Implementing a new State Store

A compliant state store needs to implement one or more interfaces: `StateStore` and `TransactionalStateStore`.

The interface for StateStore:

```
type StateStore interface {
	Init(metadata Metadata) error
	Delete(req *DeleteRequest) error
	BulkDelete(req []DeleteRequest) error
	Get(req *GetRequest) (*GetResponse, error)
	Set(req *SetRequest) error
	BulkSet(req []SetRequest) error
}
```

The interface for TransactionalStateStore:

```
type TransactionalStateStore interface {
	Init(metadata Metadata) error
	Multi(reqs []TransactionalRequest) error
}
```
