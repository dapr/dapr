# State Stores

State Stores provide a common way to interact with different data store implementations, and allow users to opt-in to advanced capabilities using defined metadata.

Currently supported state stores are:

* Azure CosmosDB
* Azure Table Storage
* Cassandra
* Cloud Firestore (Datastore mode)
* CloudState
* Couchbase
* Etcd
* HashiCorp Consul
* Hazelcast
* Memcached
* MongoDB
* Redis
* SQL Server
* Zookeeper

## Implementing a new State Store

A compliant state store needs to implement one or more interfaces: `Store` and `TransactionalStore`.

The interface for Store:

```
type Store interface {
	Init(metadata Metadata) error
	Delete(req *DeleteRequest) error
	BulkDelete(req []DeleteRequest) error
	Get(req *GetRequest) (*GetResponse, error)
	Set(req *SetRequest) error
	BulkSet(req []SetRequest) error
}
```

The interface for TransactionalStore:

```
type TransactionalStore interface {
	Init(metadata Metadata) error
	Multi(reqs []TransactionalRequest) error
}
```

See the [documentation repo](https://github.com/dapr/docs/tree/master/howto) for examples.  
