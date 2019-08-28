package documentdb

// Iterator allows easily fetch multiple result sets when response max item limit is reacheds
type Iterator struct {
	continuationToken string
	err               error
	response          *Response
	next              bool
	source            IteratorFunc
	db                *DocumentDB
}

// NewIterator creates iterator instance
func NewIterator(db *DocumentDB, source IteratorFunc) *Iterator {
	return &Iterator{
		source: source,
		db:     db,
		next:   true,
	}
}

// Response returns *Response object from last call
func (di *Iterator) Response() *Response {
	return di.response
}

// Errror returns error from last call
func (di *Iterator) Error() error {
	return di.err
}

// Next will ask iterator source for results and checks whenever there some more pages left
func (di *Iterator) Next() bool {
	if !di.next {
		return false
	}
	di.response, di.err = di.source(di.db, Continuation(di.continuationToken))
	if di.err != nil {
		return false
	}
	di.continuationToken = di.response.Continuation()
	next := di.next
	di.next = di.continuationToken != ""
	return next
}

// IteratorFunc is type that describes iterator source
type IteratorFunc func(db *DocumentDB, internalOpts ...CallOption) (*Response, error)

// NewDocumentIterator creates iterator source for fetching documents
func NewDocumentIterator(coll string, query *Query, docs interface{}, opts ...CallOption) IteratorFunc {
	return func(db *DocumentDB, internalOpts ...CallOption) (*Response, error) {
		return db.QueryDocuments(coll, query, docs, append(opts, internalOpts...)...)
	}
}
