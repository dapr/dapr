package sessions

import (
	"container/list"
	"sync"
	"time"
)

type (
	// Provider contains the sessions memory store and any external databases
	Provider struct {
		mu        sync.Mutex
		sessions  map[string]*list.Element // underline TEMPORARY memory store used to give advantage on sessions used more times than others
		list      *list.List               // for GC
		databases []Database
		Expires   time.Duration
	}
)

// NewProvider returns a new sessions provider
func NewProvider(expires time.Duration) *Provider {
	return &Provider{list: list.New(), sessions: make(map[string]*list.Element, 0), databases: make([]Database, 0), Expires: expires}
}

// RegisterDatabase adds a session database
// a session db doesn't have write access
func (p *Provider) RegisterDatabase(db Database) {
	p.mu.Lock() // for any case
	p.databases = append(p.databases, db)
	p.mu.Unlock()
}

// NewSession returns a new session from sessionid
func (p *Provider) NewSession(sid string) Session {

	sess := &session{
		sid:              sid,
		provider:         p,
		lastAccessedTime: time.Now(),
		values:           p.loadSessionValues(sid),
	}
	if p.Expires > 0 { // if not unlimited life duration and no -1 (cookie remove action is based on browser's session)
		time.AfterFunc(p.Expires, func() {
			// the destroy makes the check if this session is exists then or not,
			// this is used to destroy the session from the server-side also
			// it's good to have here for security reasons, I didn't add it on the gc function to separate its action
			p.Destroy(sid)

		})
	}

	return sess

}

func (p *Provider) loadSessionValues(sid string) map[string]interface{} {

	for i, n := 0, len(p.databases); i < n; i++ {
		if dbValues := p.databases[i].Load(sid); dbValues != nil && len(dbValues) > 0 {
			return dbValues // return the first non-empty from the registered stores.
		}
	}
	values := make(map[string]interface{})
	return values
}

func (p *Provider) updateDatabases(sid string, newValues map[string]interface{}) {
	for i, n := 0, len(p.databases); i < n; i++ {
		p.databases[i].Update(sid, newValues)
	}
}

// Init creates the session  and returns it
func (p *Provider) Init(sid string) Session {
	newSession := p.NewSession(sid)
	elem := p.list.PushBack(newSession)
	p.mu.Lock()
	p.sessions[sid] = elem
	p.mu.Unlock()
	return newSession
}

// Read returns the store which sid parameter is belongs
func (p *Provider) Read(sid string) Session {
	p.mu.Lock()
	if elem, found := p.sessions[sid]; found {
		p.mu.Unlock() // yes defer is slow
		elem.Value.(*session).lastAccessedTime = time.Now()
		return elem.Value.(*session)
	}
	p.mu.Unlock()
	// if not found create new
	sess := p.Init(sid)
	return sess
}

// Destroy destroys the session, removes all sessions values, the session itself and updates the registered session databases, this called from sessionManager which removes the client's cookie also.
func (p *Provider) Destroy(sid string) {
	p.mu.Lock()
	if elem, found := p.sessions[sid]; found {
		sess := elem.Value.(*session)
		sess.values = nil
		p.updateDatabases(sid, nil)
		delete(p.sessions, sid)
		p.list.Remove(elem)
	}
	p.mu.Unlock()
}

// Update updates the lastAccessedTime, and moves the memory place element to the front
// always returns a nil error, for now
func (p *Provider) update(sid string) {
	p.mu.Lock()
	if elem, found := p.sessions[sid]; found {
		sess := elem.Value.(*session)
		sess.lastAccessedTime = time.Now()
		p.list.MoveToFront(elem)
		p.updateDatabases(sid, sess.values)
	}
	p.mu.Unlock()
}

// GC clears the memory
func (p *Provider) GC(duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		elem := p.list.Back()
		if elem == nil {
			break
		}

		// if the time has passed. session was expired, then delete the session and its memory place
		// we are not destroy the session completely for the case this is re-used after
		sess := elem.Value.(*session)
		if time.Now().After(sess.lastAccessedTime.Add(duration)) {
			p.list.Remove(elem)
		} else {
			break
		}
	}
}
