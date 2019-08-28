package documentdb

import (
	"bytes"
	"io"
	"net/http"
)

type Clienter interface {
	Read(link string, ret interface{}, opts ...CallOption) (*Response, error)
	Delete(link string, opts ...CallOption) (*Response, error)
	Query(link string, query *Query, ret interface{}, opts ...CallOption) (*Response, error)
	Create(link string, body, ret interface{}, opts ...CallOption) (*Response, error)
	Upsert(link string, body, ret interface{}, opts ...CallOption) (*Response, error)
	Replace(link string, body, ret interface{}, opts ...CallOption) (*Response, error)
	Execute(link string, body, ret interface{}, opts ...CallOption) (*Response, error)
}

type Client struct {
	Url    string
	Config *Config
	http.Client
}

func (c *Client) apply(r *Request, opts []CallOption) (err error) { 
	if err = r.DefaultHeaders(c.Config.MasterKey); err != nil {
		return err
	}

	for i := 0; i < len(opts); i++ {
		if err = opts[i](r); err != nil {
			return err
		}
	}
	return nil
}

// Read resource by self link
func (c *Client) Read(link string, ret interface{}, opts ...CallOption) (*Response, error) {
	buf := buffers.Get().(*bytes.Buffer)
	buf.Reset()
	res, err := c.method(http.MethodGet, link, expectStatusCode(http.StatusOK), ret, buf, opts...)

	buffers.Put(buf)

	return res, err
}

// Delete resource by self link
func (c *Client) Delete(link string, opts ...CallOption) (*Response, error) {
	return c.method(http.MethodDelete, link, expectStatusCode(http.StatusNoContent), nil, &bytes.Buffer{}, opts...)
}

// Query resource
func (c *Client) Query(link string, query *Query, ret interface{}, opts ...CallOption) (*Response, error) {
	var (
		err error
		req *http.Request
		buf = buffers.Get().(*bytes.Buffer)
	)
	buf.Reset()
	defer buffers.Put(buf)

	if err = Serialization.EncoderFactory(buf).Encode(query); err != nil {
		return nil, err

	}

	req, err = http.NewRequest(http.MethodPost, c.Url+"/"+link, buf)
	if err != nil {
		return nil, err
	}
	r := ResourceRequest(link, req)

	if err = c.apply(r, opts); err != nil {
		return nil, err
	}

	r.QueryHeaders(buf.Len())

	return c.do(r, expectStatusCode(http.StatusOK), ret)
}

// Create resource
func (c *Client) Create(link string, body, ret interface{}, opts ...CallOption) (*Response, error) {
	data, err := stringify(body)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(data)
	return c.method(http.MethodPost, link, expectStatusCode(http.StatusCreated), ret, buf, opts...)
}

// Upsert resource
func (c *Client) Upsert(link string, body, ret interface{}, opts ...CallOption) (*Response, error) {
	opts = append(opts, Upsert())
	data, err := stringify(body)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(data)
	return c.method(http.MethodPost, link, expectStatusCodeXX(http.StatusOK), ret, buf, opts...)
}

// Replace resource
func (c *Client) Replace(link string, body, ret interface{}, opts ...CallOption) (*Response, error) {
	data, err := stringify(body)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(data)
	return c.method(http.MethodPut, link, expectStatusCode(http.StatusOK), ret, buf, opts...)
}

// Replace resource
// TODO: DRY, move to methods instead of actions(POST, PUT, ...)
func (c *Client) Execute(link string, body, ret interface{}, opts ...CallOption) (*Response, error) {
	data, err := stringify(body)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(data)
	return c.method(http.MethodPost, link, expectStatusCode(http.StatusOK), ret, buf, opts...)
}

// Private generic method resource
func (c *Client) method(method string, link string, validator statusCodeValidatorFunc, ret interface{}, body *bytes.Buffer, opts ...CallOption) (*Response, error) {
	req, err := http.NewRequest(method, c.Url+"/"+link, body)
	if err != nil {
		return nil, err
	}

	r := ResourceRequest(link, req)

	if err = c.apply(r, opts); err != nil {
		return nil, err
	}

	return c.do(r, validator, ret)
}

// Private Do function, DRY
func (c *Client) do(r *Request, validator statusCodeValidatorFunc, data interface{}) (*Response, error) {
	resp, err := c.Do(r.Request)
	if err != nil {
		return nil, err
	}
	if !validator(resp.StatusCode) {
		err = &RequestError{}
		readJson(resp.Body, &err)
		return nil, err
	}
	defer resp.Body.Close()
	if data == nil {
		return nil, nil
	}
	return &Response{resp.Header}, readJson(resp.Body, data)
}

// Read json response to given interface(struct, map, ..)
func readJson(reader io.Reader, data interface{}) error {
	return Serialization.DecoderFactory(reader).Decode(&data)
}

// Stringify body data
func stringify(body interface{}) (bt []byte, err error) {
	switch t := body.(type) {
	case string:
		bt = []byte(t)
	case []byte:
		bt = t
	default:
		bt, err = Serialization.Marshal(t)
	}
	return
}
