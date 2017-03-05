package logreplay

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type statusHandler int

type counterHandler int

type recorderHandler struct {
	recorder
}

type limitHandler struct {
	limit  int
	notify signalChannel
}

type slowMotionHandler struct {
	signal signalChannel
}

type redirectHandler struct {
	location string
}

type contentLengthHandler int

type headerCaptureHandler struct {
	header http.Header
}

type logReader struct {
	text string
}

type testJSONParser struct {
	test *testing.T
}

var (
	ok = statusHandler(http.StatusOK)
)

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

func (l *limitHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	l.limit--
	if l.limit == 0 {
		l.notify <- token
	}
}

func (s *slowMotionHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	<-s.signal
}

func (rh *redirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Location", rh.location)
	w.WriteHeader(http.StatusFound)
}

func (c *contentLengthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	*c += contentLengthHandler(len(b))
}

func (hc *headerCaptureHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	hc.header = r.Header
}

func chainHandlers(h ...http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, hi := range h {
			hi.ServeHTTP(w, r)
		}
	})
}

func (l *logReader) Read(p []byte) (int, error) {
	if l.text == "" {
		return 0, io.EOF
	}

	n := copy(p, l.text)
	l.text = l.text[n:]
	return n, nil
}

func (p *testJSONParser) Parse(line string) Request {
	var (
		req Request
		m   map[string]string
	)

	err := json.Unmarshal([]byte(line), &m)
	if err != nil {
		p.test.Error(err)
		return req
	}

	req.Method = m["method"]
	req.Host = m["host"]
	req.Path = m["path"]

	return req
}

func play(t *testing.T, p *Player) {
	if err := p.Play(); err != nil {
		t.Error(err)
	}
}

func once(t *testing.T, p *Player) {
	if err := p.Once(); err != nil {
		t.Error(err)
	}
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

	err = p.Once()
	if err != nil {
		t.Error(err)
	}

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

	err = p.Once()
	if err != nil {
		t.Error(err)
	}

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

	err = p.Once()
	if err != nil {
		t.Error(err)
	}

	rh.check(t, [][]string{{
		"GET", "www.example.org", "/foo",
	}, {
		"POST", "api.example.org", "/api/foo",
	}, {
		"GET", "www.example.org", "/bar",
	}})
}

func TestCustomParser(t *testing.T) {
	const logs = `
{"method": "GET", "host": "www.example.org", "path": "/foo"}
{"method": "POST", "host": "api.example.org", "path": "/api/foo"}
{"method": "GET", "host": "www.example.org", "path": "/bar"}
	`

	const invalidFormatToIgnore = "\\"

	rh := &recorderHandler{}
	s := httptest.NewServer(rh)
	defer s.Close()

	p, err := New(Options{
		AccessLog:       &logReader{logs},
		AccessLogFormat: invalidFormatToIgnore,
		Parser:          &testJSONParser{t},
		Server:          s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Once()
	if err != nil {
		t.Error(err)
	}

	rh.check(t, [][]string{{
		"GET", "www.example.org", "/foo",
	}, {
		"POST", "api.example.org", "/api/foo",
	}, {
		"GET", "www.example.org", "/bar",
	}})
}

func TestInfiniteLoop(t *testing.T) {
	const logs = `
GET /foo www.example.org
POST /api/foo api.example.org
GET /bar www.example.org
	`

	const format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`

	notify := make(signalChannel)

	lh := &limitHandler{limit: 12, notify: notify}
	s := httptest.NewServer(lh)
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

	go play(t, p)

	select {
	case <-notify:
	case <-time.After(120 * time.Millisecond):
		t.Error("timeout")
	}

	p.Stop()
}

func TestErrorOnNoRequests(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Error(err)
		return
	}

	if p.Play() != ErrNoRequests {
		t.Error("failed to fail")
	}
}

func TestCombined(t *testing.T) {
	const logs = `
GET /foo www.example.org
POST /api/foo api.example.org
GET /bar www.example.org
	`

	const format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`

	requests := []Request{{
		Method: "PUT",
		Host:   "www.example.org",
		Path:   "/foo",
	}, {
		Method: "GET",
		Host:   "api.example.org",
		Path:   "/api/foo",
	}, {
		Method: "POST",
		Host:   "www.example.org",
		Path:   "/bar",
	}}

	notify := make(signalChannel)
	s := httptest.NewServer(&limitHandler{notify: notify, limit: 18})
	defer s.Close()

	p, err := New(Options{
		AccessLog:       &logReader{logs},
		AccessLogFormat: format,
		Requests:        requests,
		Server:          s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	go play(t, p)
	p.Stop()
	go play(t, p)

	<-notify
	p.Stop()
}

func TestDoesNotFollowRedirects(t *testing.T) {
	notify := make(signalChannel)
	s := httptest.NewServer(chainHandlers(&limitHandler{notify: notify, limit: 2}, &redirectHandler{"/bar"}))
	defer s.Close()

	p, err := New(Options{Requests: []Request{{}}, Server: s.URL})
	if err != nil {
		t.Error(err)
		return
	}

	done := make(signalChannel)
	go func() {
		err := p.Once()
		if err != nil {
			t.Error(err)
		}

		done <- token
	}()

	select {
	case <-notify:
		t.Error("failed to stop redirect")
	case <-done:
	}
}

func TestFollowSameHostOnly(t *testing.T) {
	var c1 counterHandler
	s1 := httptest.NewServer(&c1)
	defer s1.Close()

	var c2 counterHandler
	s2 := httptest.NewServer(chainHandlers(&c2, &redirectHandler{s1.URL}))
	defer s2.Close()

	var c3 counterHandler
	notify := make(signalChannel)
	s3 := httptest.NewServer(chainHandlers(
		&c3,
		&limitHandler{notify: notify, limit: 2},
		&redirectHandler{"/bar"},
	))
	defer s3.Close()

	requests := []Request{{
		Host: s2.URL,
	}, {
		Host: s3.URL,
	}}
	p, err := New(Options{
		Requests:         requests,
		RedirectBehavior: FollowSameHost,
	})

	if err != nil {
		t.Error(err)
		return
	}

	// ignoring errors:
	go p.Once()

	<-notify

	if c1 != 0 || c2 != 1 || c3 != 2 {
		t.Error("failed to apply redirect behavior")
	}
}

func TestFollowRedirect(t *testing.T) {
	var c1 counterHandler
	s1 := httptest.NewServer(&c1)
	defer s1.Close()

	var c2 counterHandler
	s2 := httptest.NewServer(chainHandlers(&c2, &redirectHandler{s1.URL}))
	defer s2.Close()

	requests := []Request{{
		Host: s2.URL,
	}}
	p, err := New(Options{
		Requests:         requests,
		RedirectBehavior: FollowRedirect,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Once()
	if err != nil {
		t.Error(err)
	}

	if c1 != 1 || c2 != 1 {
		t.Error("failed to apply redirect behavior", c1, c2)
	}
}

func TestStopsOnError(t *testing.T) {
	s := httptest.NewServer(statusHandler(http.StatusOK))
	s.Close()

	p, err := New(Options{
		Requests:      []Request{{}},
		Server:        s.URL,
		HaltThreshold: 3,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Play()
	if err != ErrReqeustError {
		t.Error("failed to fail with the right error", err)
	}
}

func TestStopsOn5xx(t *testing.T) {
	s := httptest.NewServer(statusHandler(http.StatusInternalServerError))
	defer s.Close()

	p, err := New(Options{
		Requests:      []Request{{}},
		Server:        s.URL,
		HaltThreshold: 3,
		HaltOn500:     true,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Play()
	if err != ErrServerError {
		t.Error("failed to fail with the right error", err)
	}
}

func TestStopsOnErrorInOnce(t *testing.T) {
	s := httptest.NewServer(statusHandler(http.StatusOK))
	s.Close()

	p, err := New(Options{
		Requests:      []Request{{}, {}, {}},
		Server:        s.URL,
		HaltThreshold: 2,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Once()
	if err != ErrReqeustError {
		t.Error("failed to fail with the right error", err)
	}
}

func TestStopsOn5xxInOnce(t *testing.T) {
	s := httptest.NewServer(statusHandler(http.StatusInternalServerError))
	defer s.Close()

	p, err := New(Options{
		Requests:      []Request{{}, {}, {}},
		Server:        s.URL,
		HaltThreshold: 3,
		HaltOn500:     true,
	})

	if err != nil {
		t.Error(err)
		return
	}

	err = p.Once()
	if err != ErrServerError {
		t.Error("failed to fail with the right error", err)
	}
}

func TestRequestWithContent(t *testing.T) {
	var cl contentLengthHandler
	s := httptest.NewServer(&cl)
	defer s.Close()

	p, err := New(Options{
		Requests: []Request{{
			ContentLength:          500,
			ContentLengthDeviation: 0.1,
		}},
		Server: s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	once(t, p)

	if cl < 450 || cl > 550 {
		t.Error("failed to send the right content", cl)
	}
}

func TestAccessLogWithContent(t *testing.T) {
	const (
		log    = `POST /foo www.example.org`
		format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`
	)

	hc := &headerCaptureHandler{}
	var cl contentLengthHandler
	s := httptest.NewServer(chainHandlers(hc, &cl))
	defer s.Close()

	p, err := New(Options{
		AccessLog:                  &logReader{log},
		AccessLogFormat:            format,
		PostContentLength:          500,
		PostContentLengthDeviation: 0.1,
		PostSetContentLength:       true,
		Server:                     s.URL,
	})

	if err != nil {
		t.Error(err)
		return
	}

	once(t, p)

	if hc.header.Get("Content-Length") != strconv.Itoa(int(cl)) {
		t.Error("invalid content length")
	}

	if cl < 450 || cl > 550 {
		t.Error("failed to send the right content", cl)
	}
}
