package sessions

// Database is the interface which all session databases should implement
// By design it doesn't support any type of cookie session like other frameworks, I want to protect you, believe me, no context access (although we could)
// The scope of the database is to session somewhere the sessions in order to keep them after restarting the server, nothing more.
// the values are sessiond by the underline session, the check for new sessions, or 'this session value should added' are made automatically by q, you are able just to set the values to your backend database with Load function.
// session database doesn't have any write or read access to the session, the loading of the initial data is done by the Load(string) map[string]interfface{} function
// synchronization are made automatically, you can register more than one session database but the first non-empty Load return data will be used as the session values.
type Database interface {
	Load(string) map[string]interface{}
	Update(string, map[string]interface{})
}
