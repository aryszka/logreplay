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

func (r *recorderHandler) check(t *testing.T, expected [][]string) {
	if len(r.logs) != len(expected) {
		t.Error("unexpected log recorded", len(r.logs), len(expected))
	}

	for i, li := range expected {
		for j, lij := range li {
			// +1 to ignore the level
			if lij != r.logs[i][j+1] {
				t.Error("unexpected log entry", i, j, r.logs[i][j+1], lij)
			}
		}
	}
}

func (l *logReader) Read(p []byte) (int, error) {
	if l.text == "" {
		return 0, io.EOF
	}

	n := copy(p, l.text)
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

	p, err := New(Options{
		AccessLog: &logReader{accessLog},
		Server:    s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	p.Once()
	rh.check(t, [][]string{{
		"GET", "www.example.org", "/foo",
	}, {
		"POST", "api.example.org", "/api/foo",
	}, {
		"GET", "www.example.org", "/baz",
	}})
}

func TestReplayBlank(t *testing.T) {
	const requestCount = 3

	var c counterHandler
	s := httptest.NewServer(&c)
	defer s.Close()

	var reqs [requestCount]Request
	p, err := New(Options{
		Requests: reqs[:],
		Server:   s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	p.Once()
	if c != requestCount {
		t.Error("replaying requests failed", c, requestCount)
	}
}

func TestCustomFormat(t *testing.T) {
	const logs = `
GET /foo www.example.org
POST /api/foo api.example.org
GET /bar www.example.org
	`

	const format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`

	rh := &recorderHandler{}
	s := httptest.NewServer(rh)
	defer s.Close()

	p, err := New(Options{
		AccessLog:       &logReader{logs},
		AccessLogFormat: format,
		Server:          s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	p.Once()
	rh.check(t, [][]string{{
		"GET", "www.example.org", "/foo",
	}, {
		"POST", "api.example.org", "/api/foo",
	}, {
		"GET", "www.example.org", "/bar",
	}})
}
