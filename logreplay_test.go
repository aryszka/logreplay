package logreplay

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type statusHandler int

type counterHandler int

var ok = statusHandler(http.StatusOK)

func init() {
	enableDebugLog()
}

func (s statusHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(int(s))
}

func (c *counterHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	*c++
}

func TestReplayCustom(t *testing.T) {
	const requestCount = 3

	var c counterHandler
	s := httptest.NewServer(&c)
	defer s.Close()

	var reqs [requestCount]Request
	p := New(Options{
		Requests: reqs[:],
		Server:   s.URL,
	})

	p.Once()
	if c != requestCount {
		t.Error("replaying requests failed", c, requestCount)
	}
}
