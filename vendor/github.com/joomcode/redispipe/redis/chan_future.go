package redis

// ChanFutured wraps Sender and provides asynchronous interface through future implemented
// with channel.
type ChanFutured struct {
	S Sender
}

// Send sends requests and returns ChanFuture for result.
func (s ChanFutured) Send(r Request) *ChanFuture {
	f := &ChanFuture{wait: make(chan struct{})}
	s.S.Send(r, f, 0)
	return f
}

// SendMany sends several requests and returns slice of ChanFuture for results.
func (s ChanFutured) SendMany(reqs []Request) ChanFutures {
	futures := make(ChanFutures, len(reqs))
	for i := range futures {
		futures[i] = &ChanFuture{wait: make(chan struct{})}
	}
	s.S.SendMany(reqs, futures, 0)
	return futures
}

// SendTransaction sends several requests as MULTI+EXEC transaction,
// returns ChanTransaction - wrapper around ChanFuture with additional method.
func (s ChanFutured) SendTransaction(r []Request) *ChanTransaction {
	future := &ChanTransaction{
		ChanFuture: ChanFuture{wait: make(chan struct{})},
	}
	s.S.SendTransaction(r, future, 0)
	return future
}

// ChanFuture - future implemented with channel as signal of fulfillment.
type ChanFuture struct {
	r    interface{}
	wait chan struct{}
}

// Value waits for result to be fulfilled and returns result.
func (f *ChanFuture) Value() interface{} {
	<-f.wait
	return f.r
}

// Done returns channel that will be closed on fulfillment.
func (f *ChanFuture) Done() <-chan struct{} {
	return f.wait
}

// Resolve - implementation of Future.Resolve
func (f *ChanFuture) Resolve(res interface{}, _ uint64) {
	f.r = res
	close(f.wait)
}

// Cancelled - implementation of Future.Cancelled (always false).
func (f *ChanFuture) Cancelled() error {
	return nil
}

// ChanFutures - implementation of Future over slice of *ChanFuture
type ChanFutures []*ChanFuture

// Cancelled - implementation of Future.Cancelled (always false).
func (f ChanFutures) Cancelled() error {
	return nil
}

// Resolve - implementation of Future.Resolve.
// It resolves ChanFuture corresponding to index.
func (f ChanFutures) Resolve(res interface{}, i uint64) {
	f[i].Resolve(res, i)
}

// ChanTransaction - wrapper over ChanFuture with additional convenient method.
type ChanTransaction struct {
	ChanFuture
}

// Results - parses result of transaction and returns it as an array of results.
func (f *ChanTransaction) Results() ([]interface{}, error) {
	<-f.wait
	return TransactionResponse(f.r)
}
