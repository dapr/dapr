package action

type StateStore interface {
	GetAll(keyMatch string) ([]KeyValState, error)
}

type KeyValState struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}
