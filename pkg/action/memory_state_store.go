package action

type MemoryStateStore struct {
	State map[string]interface{}
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{}
}

func (m *MemoryStateStore) Init(eventSourceSpec EventSourceSpec) error {
	m.State = make(map[string]interface{})
	return nil
}

func (m *MemoryStateStore) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (m *MemoryStateStore) Read(metadata interface{}) (interface{}, error) {
	key := metadata.(string)
	return m.State[key], nil
}

func (m *MemoryStateStore) Write(data interface{}) error {
	state := data.(KeyValState)
	m.State[state.Key] = state.Value
	return nil
}
