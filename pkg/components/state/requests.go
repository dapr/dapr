package state

// GetRequest is the object describing a state fetch request
type GetRequest struct {
	Key  string `json:"key"`
	ETag string `json:"etag,omitempty"`
}

// DeleteRequest is the object describing a delete state request
type DeleteRequest struct {
	Key string `json:"key"`
}

// SetRequest is the object describing an upsert request
type SetRequest struct {
	Key      string            `json:"key"`
	Value    interface{}       `json:"value"`
	ETag     string            `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata"`
}

// OperationType describes a CRUD operation performed against a state store
type OperationType string

// Upsert is an update or create operation
const Upsert OperationType = "upsert"

// Delete is a delete operation
const Delete OperationType = "delete"

// TransactionalRequest describes a transactional operation against a state store that comprises multiple types of operations
// The Request field is either a DeleteRequest or SetRequest
type TransactionalRequest struct {
	Operation OperationType `json:"operation"`
	Request   interface{}   `json:"request"`
}
