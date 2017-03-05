// Package logreplay provides a client that can replay HTTP requests based access logs.
//
// The player can replay the request scenario once or infinitely in a loop. It can pause
// or reset the scenario. It can replay the scenario with a concurrency level of choice.
// The player accepts custom request definitions besides or instead of the access log
// input.
//
// The player is provided as an embeddable library, extended with a simple command line
// wrapper.
package logreplay

import (
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
)

// RedirectBehavior defines how to handle redirect responses.
type RedirectBehavior int

const (

	// NoFollow tells the player not to follow redirects.
	NoFollow RedirectBehavior = iota

	// SameHost tells the player to follow redirects only to the same host
	// as the request was made to.
	SameHost

	// Follow tells the player to follow all redirects. (Not recommended
	// during load tests.)
	Follow
)

// HaltPolicy defines whether to halt on failed requests. It is used in combination
// with HaltThreshold.
type HaltPolicy int

const (

	// PauseOnError tells the player to pause requests when an error occurs.
	PauseOnError HaltPolicy = iota

	// StopOnError tells the player to stop requests when an error occurs.
	StopOnError

	// PauseOn500 tells the player to pause requests when an error or a 500 response occurs.
	PauseOn500

	// StopOn500 tells the player to stop requests when an error or a 500 response occurs.
	StopOn500

	// NoHalt tells the player to ignore all request errors and 500s.
	NoHalt
)

const defaultHaltThreshold = 1 << 7

// Request describes an individual request made by the player.
type Request struct {

	// Method is the HTTP method of the request. Defaults to GET.
	Method string

	// Host is set as the Host header of the request. When no explicit server is
	// specified in the player options, the Host field will be used as the network
	// address of the request.
	Host string

	// Path is set as the HTTP path of the request.
	Path string

	// ContentLength defines the size of the randomly generated request payload.
	//
	// When ContentLengthDeviation is defined, the actual size will be randomly
	// decided by ContentLength +/- rand(ContentLengthDeviation).
	//
	// The HTTP ContentLength header is set only when Chunked is false.
	ContentLength int

	// ContentLengthDeviation defines how much the actual random content length of
	// a request can differ from ContentLength.
	ContentLengthDeviation float64

	// Chunked defines if the request content should be sent with chunked transfer
	// encoding.
	//
	// TODO: what to do with requests coming from the access logs
	Chunked bool
}

// Parser can parse a log entry.
type Parser interface {

	// Parse parses a log entry. It accepts a log line as a string
	// and returns a Request definition.
	Parse(string) Request
}

// Options is used to initialize a player.
type Options struct {

	// Requests to be executed by the player in the specified order.
	//
	// When used together with AccessLog, requests defined in this field are
	// executed in the beginning of the scenario.
	Requests []Request

	// AccessLog is a source of scenario to be executed by the player. By default, it
	// expects a stream of Apache access log entries, and uses the %r field to forge
	// requests.
	//
	// In addition to the Combined log format, the default parser accepts two additional
	// fields based on the Skipper (https://github.com/zalando/skipper) access logs,
	// where the request host is taken from the last field (following an integer for
	// duration.)
	//
	// On continuous play, the log is read only once, and stored in memory for subsequent
	// plays. For this reason, the parsed access log must fit in memory.
	//
	// Known bugs in the default parser:
	//
	// 	- escaped double quotes in the HTTP message not handled
	// 	- whitespace handling in the HTTP message: only whitespace
	//
	AccessLog io.Reader

	// AccessLogFormat is a regular expression and can be used to override the default
	// parser expression. The expression can define the following named groups:
	// method, host, path. The captured submatches with these names will be used to
	// set the according field in the parsed request.
	//
	// If Parser is set, this field is ignored.
	AccessLogFormat string

	// Parser is a custom parser for log entries (lines). It can be used e.g. to define
	// a JSON log parser.
	Parser Parser

	// AccessLogHostField tells the access log parser to take the request from a
	// specific position in the Apache Combined Log Format. Defaults to 11. Value
	// -1 prevents to set the host from the access log. When the host field not
	// specified in the access log, or when taking it from there is disabled, the
	// value of the field Server is used as the host, or, if no server is specified,
	// localhost is used.
	//
	// TODO: allow defining custom format
	AccessLogHostField int

	// Server is a network address to send the requests to.
	Server string

	// ConcurrentSessions tells the player how many concurrent clients should replay
	// the requests.
	//
	// Defaults to 1.
	ConcurrentSessions int

	// RedirectBehavior tells the player how to act on redirect responses.
	RedirectBehavior RedirectBehavior

	// PostContentLength tells the player the average request content size to send in
	// case of POST, PUT and PATCH requests read from the access log.
	PostContentLength int

	// PostContentLengthDeviation defines how much the actual random cotnent length of
	// a request read from the access log can differ from PostContentLength.
	PostContentLengthDeviation float64

	// Log defines a custom logger for the player.
	Log Logger

	// HaltPolicy tells the player what to do in case an error or a 500 response occurs.
	// The default is to pause on errors (500s ignored).
	HaltPolicy HaltPolicy

	// HaltThreshold tells the player after how many errors or 500s it should apply the
	// HaltPolicy. Default: 128.
	HaltThreshold int

	// Throttle maximizes the outgoing overall request per second rate.
	Throttle float64
}

type (
	signalToken   struct{}
	signalChannel chan signalToken
	errorChannel  chan error
)

// Player replays HTTP requests explicitly specified and/or read from an Apache access log.
type Player struct {
	options        Options
	accessLog      *reader
	logEntries     []Request
	customRequests []Request
	position       int
	client         *http.Client
	errors         int
	serverErrors   int
	notRunning     signalChannel
	signalPlay     chan errorChannel
	signalOnce     chan signalChannel
	signalPause    chan signalChannel
	signalStop     chan signalChannel
}

var (
	errServerStatus = errors.New("unexpected server status")
	errAccessLogEOF = errors.New("access log EOF")
	errNoRequests   = errors.New("no requests to play")
	token           = signalToken{}
)

// New initialzies a player.
func New(o Options) (*Player, error) {
	if o.Log == nil {
		o.Log = newDefaultLog()
	}

	var r *reader
	if o.AccessLog != nil {
		var err error
		r, err = newReader(o.AccessLog, o.AccessLogFormat, o.Parser, o.Log)
		if err != nil {
			return nil, err
		}
	}

	notRunning := make(signalChannel, 1)
	notRunning <- token

	return &Player{
		options:        o,
		accessLog:      r,
		customRequests: o.Requests,
		client:         &http.Client{Transport: &http.Transport{}},
		notRunning:     notRunning,
		signalPlay:     make(chan errorChannel),
		signalOnce:     make(chan signalChannel),
		signalPause:    make(chan signalChannel),
		signalStop:     make(chan signalChannel),
	}, nil
}

func (p *Player) nextRequest() (Request, error) {
	var r Request
	if p.position < len(p.logEntries) {
		r = p.logEntries[p.position]
		p.position++
		return r, nil
	}

	if p.accessLog == nil {
		pcustom := p.position - len(p.logEntries)
		if pcustom >= len(p.customRequests) {
			return r, io.EOF
		}

		r = p.customRequests[pcustom]
		p.position++
		return r, nil
	}

	var err error
	r, err = p.accessLog.ReadRequest()
	if err != nil && err != io.EOF {
		p.options.Log.Warnln("error while reading access log:", err)
		return r, err
	}

	if err == io.EOF {
		p.accessLog = nil
		return r, errAccessLogEOF
	}

	p.logEntries = append(p.logEntries, r)
	p.position++
	return r, nil
}

func (p *Player) createHTTPRequest(r Request) (*http.Request, error) {
	m := r.Method
	if m == "" {
		m = "GET"
	}

	a := p.options.Server
	if a == "" {
		if r.Host == "" {
			a = "http://localhost"
		} else {
			a = "http://" + r.Host
		}
	}

	u, err := url.Parse(a)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "" {
		u.Scheme = "http"
	}

	u.Path = r.Path

	hr, err := http.NewRequest(m, u.String(), nil)
	if err != nil {
		return nil, err
	}

	h := r.Host
	if h == "" {
		h = p.options.Server
	}

	if h == "" {
		h = "localhost"
	}

	hr.Host = h

	return hr, nil
}

func (p *Player) sendRequest(r Request) error {
	hr, err := p.createHTTPRequest(r)
	if err != nil {
		p.options.Log.Errorln("failed to create request", err)
		return err
	}

	rsp, err := p.client.Do(hr)
	if err != nil {
		p.options.Log.Warnln("error while making request:", err)
		return err
	}

	defer rsp.Body.Close()

	if rsp.StatusCode >= http.StatusInternalServerError {
		return errServerStatus
	}

	_, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		p.options.Log.Warnln("error while reading request body:", err)
		return err
	}

	return nil
}

func (p *Player) checkHaltError() {
	if p.errors < p.options.HaltThreshold {
		return
	}

	p.options.Log.Errorln("request errors exceeded threshold")

	switch p.options.HaltPolicy {
	case StopOnError, StopOn500:
		p.Stop()
	case PauseOnError, PauseOn500:
		p.Pause()
	}
}

func (p *Player) checkHaltStatus() {
	if p.serverErrors < p.options.HaltThreshold {
		return
	}

	p.options.Log.Errorln("server errors exceeded threshold")

	switch p.options.HaltPolicy {
	case StopOn500:
		p.Stop()
	case PauseOn500:
		p.Pause()
	}
}

func (p *Player) run() {
	var (
		once         bool
		running      signalChannel
		waiting      []signalChannel
		waitingError []errorChannel
	)

	letRun := make(signalChannel)
	close(letRun)

	stop := func() {
		p.position = 0
		p.notRunning <- token

		for _, w := range waitingError {
			close(w)
		}

		for _, w := range waiting {
			close(w)
		}
	}

	for {
		select {
		case d := <-p.signalPlay:
			waitingError = append(waitingError, d)
			once = false
			running = letRun
		case d := <-p.signalOnce:
			waiting = append(waiting, d)
			once = true
			running = letRun
		case d := <-p.signalPause:
			running = nil
			close(d)
			continue
		case d := <-p.signalStop:
			stop()
			close(d)
			return
		case <-running:
			r, err := p.nextRequest()
			if err == io.EOF {
				if once {
					stop()
					return
				}

				if p.position == 0 {
					for _, w := range waitingError {
						w <- errNoRequests
					}
				}

				p.position = 0
				continue
			}

			if err == nil {
				err = p.sendRequest(r)
			}

			switch err {
			case nil, errAccessLogEOF:
			case errServerStatus:
				p.serverErrors++
				p.checkHaltStatus()
			default:
				p.errors++
				p.checkHaltError()
			}
		}
	}
}

// this is enough to avoid starting more than one goroutine
func (p *Player) isRunning() bool {
	select {
	case <-p.notRunning:
		return false
	default:
		return true
	}
}

func (p *Player) signal(s chan signalChannel) {
	done := make(signalChannel)
	s <- done
	<-done
}

// Play replays the requests infinitely with the specified concurrency. If an access log is
// specified it reads it to the end only once, and it repeats the read requests from then on.
//
// When the player is currently playing requests, it is a noop. Play is blocking, in order to
// use Pause() or Stop(), they need to be called from a different goroutine. To cleanup
// resources, it must be stopped. Play(), Once() and Pause() can be called any number of times
// during a session started by Play() or Once().
func (p *Player) Play() {
	if !p.isRunning() {
		go p.run()
	}

	done := make(errorChannel)
	p.signalPlay <- done
	if err := <-done; err != nil {
		panic(err)
	}
}

// Once replays the requests once.
//
// When the player is currently playing requests, it is a noop. Once is blocking, in order to
// use Pause() or Stop(), they need to be called from a different goroutine. To cleanup
// resources, it must run to the end, or it must be stopped. Play(), Once() and Pause() can be
// called any number of times during a session started by Play() or Once().
func (p *Player) Once() {
	if !p.isRunning() {
		go p.run()
	}

	p.signal(p.signalOnce)
}

// Pause pauses the replay of the requests. When Play() or Once() are called after pause, the
// replay is resumed at the next request in order. It should not be called after Stop(). Play(),
// Once() and Pause() can be called any number of times during a session started by Play() or
// Once().
func (p *Player) Pause() {
	p.signal(p.signalPause)
}

// Stop stops the replay of the requests. When Play() or Once() are called after stop, the
// replay starts from the first request. It can be called only once after Play() or Once() was
// called.
func (p *Player) Stop() {
	p.signal(p.signalStop)
}
