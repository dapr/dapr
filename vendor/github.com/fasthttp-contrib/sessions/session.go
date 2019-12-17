package sessions

import (
	"sync"
	"time"
)

type (
	// Session is  session's session interface, think it like a local store of each session's values
	// implemented by the internal session iteral, normally the end-user will never use this interface.
	// gettable by the provider -> sessions -> your app
	Session interface {
		ID() string
		Get(string) interface{}
		GetString(key string) string
		GetInt(key string) int
		GetAll() map[string]interface{}
		VisitAll(cb func(k string, v interface{}))
		Set(string, interface{})
		Delete(string)
		Clear()
	}
	// session is an 'object' which wraps the session provider with its session databases, only frontend user has access to this session object.
	// implements the Session interface
	session struct {
		sid              string
		values           map[string]interface{} // here are the real values
		mu               sync.Mutex
		lastAccessedTime time.Time
		createdAt        time.Time
		provider         *Provider
	}
)

// ID returns the session's id
func (s *session) ID() string {
	return s.sid
}

// Get returns the value of an entry by its key
func (s *session) Get(key string) interface{} {
	s.provider.update(s.sid)
	if value, found := s.values[key]; found {
		return value
	}
	return nil
}

// GetString same as Get but returns as string, if nil then returns an empty string
func (s *session) GetString(key string) string {
	if value := s.Get(key); value != nil {
		if v, ok := value.(string); ok {
			return v
		}

	}

	return ""
}

// GetInt same as Get but returns as int, if nil then returns -1
func (s *session) GetInt(key string) int {
	if value := s.Get(key); value != nil {
		if v, ok := value.(int); ok {
			return v
		}
	}

	return -1
}

// GetAll returns all session's values
func (s *session) GetAll() map[string]interface{} {
	return s.values
}

// VisitAll loop each one entry and calls the callback function func(key,value)
func (s *session) VisitAll(cb func(k string, v interface{})) {
	for key := range s.values {
		cb(key, s.values[key])
	}
}

// Set fills the session with an entry, it receives a key and a value
// returns an error, which is always nil
func (s *session) Set(key string, value interface{}) {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	s.provider.update(s.sid)
}

// Delete removes an entry by its key
// returns an error, which is always nil
func (s *session) Delete(key string) {
	s.mu.Lock()
	delete(s.values, key)
	s.mu.Unlock()
	s.provider.update(s.sid)
}

// Clear removes all entries
func (s *session) Clear() {
	s.mu.Lock()
	for key := range s.values {
		delete(s.values, key)
	}
	s.mu.Unlock()
	s.provider.update(s.sid)
}
