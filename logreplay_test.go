package logreplay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type statusHandler int

type counterHandler int

type recorderHandler struct {
	recorder
}

type logReader struct {
	text string
}

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

func (r *recorderHandler) ServeHTTP(_ http.ResponseWriter, req *http.Request) {
	r.Infoln(req.Method, req.Host, req.URL.Path)
}

func (l *logReader) Read(p []byte) (int, error) {
	println("reading text")
	if l.text == "" {
		println("text empty")
		return 0, io.EOF
	}

	println("copying text")
	n := copy(p, l.text)
	println("text copied", n)
	l.text = l.text[n:]
	return n, nil
}

func TestReplayAccessLog(t *testing.T) {
	const accessLog = `
1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "GET /foo HTTP/1.1" 200 566 "https://www.example.org/bar.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 www.example.org
1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "POST /api/foo HTTP/1.1" 200 138 "https://www.example.org/bar.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 api.example.org
1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "GET /baz HTTP/1.1" 200 566 "https://www.example.org/qux.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 www.example.org`

	rh := &recorderHandler{}
	s := httptest.NewServer(rh)
	defer s.Close()

	p := New(Options{
		AccessLog: &logReader{accessLog},
		Server:    s.URL,
	})

	p.Once()
	if len(rh.logs) != 3 {
		t.Error("replaying requests failed", len(rh.logs), 3)
	}

	for i, li := range [][]string{{
		"GET", "www.example.org", "/foo",
	}, {
		"POST", "api.example.org", "/api/foo",
	}, {
		"GET", "www.example.org", "/baz",
	}} {
		for j, lij := range li {
			if lij != rh.logs[i][j+1] {
				t.Error("wrong request made", i, j, rh.logs[i][j], lij)
			}
		}
	}
}

func TestReplayBlank(t *testing.T) {
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
