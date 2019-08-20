package state

type TransactionalStateStore interface {
	Init(metadata Metadata) error
	Multi(reqs []TransactionalRequest) error
}
