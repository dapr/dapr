package action

import (
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/actionscore/actions/e2e/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestInvokeActionGETWithOneParameter(t *testing.T) {
	is := assert.New(t)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("http://localhost:3505/action/test-action/echo?param=1")
	req.Header.Set("actions.action-address", "localhost:60006")
	req.Header.SetMethod("GET")
	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}
	client.Do(req, resp)
	bodyBytes := resp.Body()
	is.Equal(string(bodyBytes), "param=1;")
}
func TestInvokeActionGETWithTwoParameters(t *testing.T) {
	is := assert.New(t)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("http://localhost:3505/action/test-action/echo?param1=1&param2=2")
	req.Header.Set("actions.action-address", "localhost:60006")
	req.Header.SetMethod("GET")
	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}
	client.Do(req, resp)
	bodyBytes := resp.Body()
	body := string(bodyBytes)
	is.True(body == "param1=1;param2=2;" || body == "param2=2;param1=1;")
}

func TestInvokeActionGETWithEncodedParameter(t *testing.T) {
	is := assert.New(t)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("http://localhost:3505/action/test-action/echo?param=%24%26%2b%2c%2f%3a%3b%3d%3f%40")
	req.Header.Set("actions.action-address", getIP()+":60006")
	req.Header.SetMethod("GET")
	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}
	client.Do(req, resp)
	bodyBytes := resp.Body()
	is.Equal(string(bodyBytes), "param=$&+,/:;=?@;")
}

func TestInvokeActionGETWithNoParameters(t *testing.T) {
	is := assert.New(t)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("http://localhost:3505/action/test-action/echo")
	req.Header.Set("actions.action-address", "localhost:60006")
	req.Header.SetMethod("GET")
	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}
	client.Do(req, resp)
	bodyBytes := resp.Body()
	is.Equal(string(bodyBytes), "")
}

func TestMain(m *testing.M) {
	launchAndWait(func(port int, appPort string, grpcPort int) {
		app := mocks.NewMockApp(true, 100, true)
		app.Run(port)
	}, 8089, "", 0)

	launchAndWait(func(port int, appPort string, grpcPort int) {
		i := NewAction("test-action", appPort, "standalone", "http", "", "", "", "", "")
		i.Run(port, grpcPort)
	}, 3505, "8089", 60006)
	os.Exit(m.Run())
}

type launcher func(int, string, int)

func launchAndWait(fn launcher, port int, appPort string, grpcPort int) {
	go func(port int, appPort string, grpcPort int) {
		fn(port, appPort, grpcPort)
	}(port, appPort, grpcPort)
	timeout := time.Duration(2) * time.Second
	for {
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("", strconv.Itoa(port)), timeout)
		if conn != nil {
			conn.Close()
			break
		}
	}
}
func getIP() string {
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	return "127.0.0.1"
}
