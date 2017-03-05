package logreplay

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

type statusHandler int

type counterHandler struct {
	mx      sync.Mutex
	counter int
}

type recorderHandler struct {
	mx sync.Mutex
	recorder
}

type limitHandler struct {
	mx     sync.Mutex
	limit  int
	notify signalChannel
}

type slowMotionHandler struct {
	signal signalChannel
}

type redirectHandler struct {
	location   string
	unlessPath string
}

type contentLengthHandler struct {
	mx     sync.Mutex
	length int
}

type headerCaptureHandler struct {
	mx     sync.Mutex
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
	c.mx.Lock()
	defer c.mx.Unlock()
	c.counter++
}

func (r *recorderHandler) ServeHTTP(_ http.ResponseWriter, req *http.Request) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.Infoln(req.Method, req.Host, req.URL.Path)
}

func (r *recorderHandler) checkLength(t *testing.T, expected int) {
	if len(r.logs) != expected {
		t.Error("unexpected log recorded", len(r.logs), expected)
	}
}

func (r *recorderHandler) check(t *testing.T, expected [][]string) {
	r.checkLength(t, len(expected))

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
	l.mx.Lock()
	defer l.mx.Unlock()
	l.limit--
	if l.limit == 0 {
		l.notify <- token
	}
}

func (s *slowMotionHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	<-s.signal
}

func (rh *redirectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rh.unlessPath != "" && r.URL.Path == rh.unlessPath {
		return
	}

	w.Header().Set("Location", rh.location)
	w.WriteHeader(http.StatusFound)
}

func (c *contentLengthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mx.Lock()
	defer c.mx.Unlock()

	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	c.length += len(b)
}

func (hc *headerCaptureHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	hc.mx.Lock()
	defer hc.mx.Unlock()
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

func (p *testJSONParser) Parse(line string) *Request {
	var m map[string]string
	err := json.Unmarshal([]byte(line), &m)
	if err != nil {
		p.test.Error(err)
		return nil
	}

	return &Request{
		Method: m["method"],
		Host:   m["host"],
		Path:   m["path"],
	}
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

func test(t *testing.T, concurrency int) {
	t.Run("TestReplayAccessLog", func(t *testing.T) {
		const accessLog = `
			1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "GET /foo HTTP/1.1" 200 566 "https://www.example.org/bar.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 www.example.org
			1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "POST /api/foo HTTP/1.1" 200 138 "https://www.example.org/bar.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 api.example.org
			1.2.3.4, 5.6.7.8, 9.0.1.2 - - [02/Mar/2017:11:43:00 +0000] "GET /baz HTTP/1.1" 200 566 "https://www.example.org/qux.html", "Mozilla/5.0 (iPhone; CPU iPHone OS 10_2_1 like Mac OS X) AppleWebKit/600.1.4 (KHTML, like Gecko) GSA/23.0.1234 Mobile/14D27 Safari/600.1.4" 1 www.example.org`

		rh := &recorderHandler{}
		s := httptest.NewServer(rh)
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			AccessLog:          &logReader{accessLog},
			Server:             s.URL,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != nil {
			t.Error(err)
		}

		if concurrency > 1 {
			rh.checkLength(t, 3*concurrency)
			return

		}

		rh.check(t, [][]string{{
			"GET", "www.example.org", "/foo",
		}, {
			"POST", "api.example.org", "/api/foo",
		}, {
			"GET", "www.example.org", "/baz",
		}})
	})

	t.Run("TestReplayBlank", func(t *testing.T) {
		const requestCount = 3

		c := &counterHandler{}
		s := httptest.NewServer(c)
		defer s.Close()

		reqs := make([]*Request, requestCount)
		for i := 0; i < requestCount; i++ {
			reqs[i] = &Request{}
		}

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           reqs,
			Server:             s.URL,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != nil {
			t.Error(err)
		}

		if int(c.counter) != concurrency*requestCount {
			t.Error("replaying requests failed", c.counter, concurrency*requestCount)
		}
	})

	t.Run("TestCustomFormat", func(t *testing.T) {
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
			ConcurrentSessions: concurrency,
			AccessLog:          &logReader{logs},
			AccessLogFormat:    format,
			Server:             s.URL,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != nil {
			t.Error(err)
		}

		if concurrency > 1 {
			rh.checkLength(t, 3*concurrency)
			return

		}

		rh.check(t, [][]string{{
			"GET", "www.example.org", "/foo",
		}, {
			"POST", "api.example.org", "/api/foo",
		}, {
			"GET", "www.example.org", "/bar",
		}})
	})

	t.Run("TestCustomParser", func(t *testing.T) {
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
			ConcurrentSessions: concurrency,
			AccessLog:          &logReader{logs},
			AccessLogFormat:    invalidFormatToIgnore,
			Parser:             &testJSONParser{t},
			Server:             s.URL,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != nil {
			t.Error(err)
		}

		if concurrency > 1 {
			rh.checkLength(t, 3*concurrency)
			return

		}

		rh.check(t, [][]string{{
			"GET", "www.example.org", "/foo",
		}, {
			"POST", "api.example.org", "/api/foo",
		}, {
			"GET", "www.example.org", "/bar",
		}})
	})

	t.Run("TestInfiniteLoop", func(t *testing.T) {
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
			ConcurrentSessions: concurrency,
			AccessLog:          &logReader{logs},
			AccessLogFormat:    format,
			Server:             s.URL,
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
	})

	t.Run("TestErrorOnNoRequests", func(t *testing.T) {
		p, err := New(Options{ConcurrentSessions: concurrency})
		if err != nil {
			t.Error(err)
			return
		}

		if err := p.Play(); err != ErrNoRequests {
			t.Error("failed to fail", err)
		}
	})

	t.Run("TestCombined", func(t *testing.T) {
		const logs = `
			GET /foo www.example.org
			POST /api/foo api.example.org
			GET /bar www.example.org
		`

		const format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`

		requests := []*Request{{
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
			ConcurrentSessions: concurrency,
			AccessLog:          &logReader{logs},
			AccessLogFormat:    format,
			Requests:           requests,
			Server:             s.URL,
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
	})

	t.Run("TestDoesNotFollowRedirects", func(t *testing.T) {
		notify := make(signalChannel)
		s := httptest.NewServer(chainHandlers(
			&limitHandler{notify: notify, limit: concurrency * 2},
			&redirectHandler{location: "/bar"},
		))
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           []*Request{{}},
			Server:             s.URL,
		})
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
	})

	t.Run("TestFollowSameHostOnly", func(t *testing.T) {
		c1 := &counterHandler{}
		s1 := httptest.NewServer(c1)
		defer s1.Close()

		c2 := &counterHandler{}
		s2 := httptest.NewServer(chainHandlers(c2, &redirectHandler{location: s1.URL}))
		defer s2.Close()

		c3 := &counterHandler{}
		notify := make(signalChannel)
		s3 := httptest.NewServer(chainHandlers(
			c3,
			&limitHandler{notify: notify, limit: 2 * concurrency},
			&redirectHandler{
				location:   "/bar",
				unlessPath: "/bar",
			},
		))
		defer s3.Close()

		requests := []*Request{{
			Host: s2.URL,
		}, {
			Host: s3.URL,
		}}
		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           requests,
			RedirectBehavior:   FollowSameHost,
		})

		if err != nil {
			t.Error(err)
			return
		}

		// ignoring errors:
		go p.Once()

		<-notify

		if c1.counter != 0 || c2.counter != concurrency || c3.counter != 2*concurrency {
			t.Error("failed to apply redirect behavior", c1.counter, c2.counter, c3.counter)
		}
	})

	t.Run("TestFollowRedirect", func(t *testing.T) {
		c1 := &counterHandler{}
		s1 := httptest.NewServer(c1)
		defer s1.Close()

		c2 := &counterHandler{}
		s2 := httptest.NewServer(chainHandlers(c2, &redirectHandler{location: s1.URL}))
		defer s2.Close()

		requests := []*Request{{
			Host: s2.URL,
		}}
		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           requests,
			RedirectBehavior:   FollowRedirect,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != nil {
			t.Error(err)
		}

		if c1.counter != concurrency || c2.counter != concurrency {
			t.Error("failed to apply redirect behavior", c1.counter, c2.counter)
		}
	})

	t.Run("TestStopsOnError", func(t *testing.T) {
		s := httptest.NewServer(statusHandler(http.StatusOK))
		s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           []*Request{{}},
			Server:             s.URL,
			HaltThreshold:      3,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Play()
		if err != ErrRequestError {
			t.Error("failed to fail with the right error", err)
		}
	})

	t.Run("TestStopsOn5xx", func(t *testing.T) {
		s := httptest.NewServer(statusHandler(http.StatusInternalServerError))
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           []*Request{{}},
			Server:             s.URL,
			HaltThreshold:      3,
			HaltOn500:          true,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Play()
		if err != ErrServerError {
			t.Error("failed to fail with the right error", err)
		}
	})

	t.Run("TestStopsOnErrorInOnce", func(t *testing.T) {
		s := httptest.NewServer(statusHandler(http.StatusOK))
		s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           []*Request{{}, {}, {}},
			Server:             s.URL,
			HaltThreshold:      2,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != ErrRequestError {
			t.Error("failed to fail with the right error", err)
		}
	})

	t.Run("TestStopsOn5xxInOnce", func(t *testing.T) {
		s := httptest.NewServer(statusHandler(http.StatusInternalServerError))
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests:           []*Request{{}, {}, {}},
			Server:             s.URL,
			HaltThreshold:      2,
			HaltOn500:          true,
		})

		if err != nil {
			t.Error(err)
			return
		}

		err = p.Once()
		if err != ErrServerError {
			t.Error("failed to fail with the right error", err)
		}
	})

	t.Run("TestRequestWithContent", func(t *testing.T) {
		cl := &contentLengthHandler{}
		s := httptest.NewServer(cl)
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions: concurrency,
			Requests: []*Request{{
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

		if cl.length < 450*concurrency || cl.length > 550*concurrency {
			t.Error("failed to send the right content", cl.length)
		}
	})

	t.Run("TestAccessLogWithContent", func(t *testing.T) {
		const (
			log    = `POST /foo www.example.org`
			format = `^(?P<method>\S+)\s+(?P<path>\S+)\s+(?P<host>\S+)$`
		)

		hc := &headerCaptureHandler{}
		cl := &contentLengthHandler{}
		s := httptest.NewServer(chainHandlers(hc, cl))
		defer s.Close()

		p, err := New(Options{
			ConcurrentSessions:         concurrency,
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

		if concurrency == 1 && hc.header.Get("Content-Length") != strconv.Itoa(cl.length) {
			t.Error("invalid content length")
		}

		if cl.length < concurrency*450 || cl.length > concurrency*550 {
			t.Error("failed to send the right content", cl.length)
		}
	})
}

func TestConcurrency1(t *testing.T) { test(t, 1) }
func TestConcurrency2(t *testing.T) { test(t, 2) }
func TestConcurrency4(t *testing.T) { test(t, 4) }

func TestConcurrency15(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	test(t, 15)
}

func TestConcurrency243(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	test(t, 243)
}
