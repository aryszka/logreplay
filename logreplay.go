// Package logreplay provides a client that can replay HTTP requests based access logs.
//
// The player can replay the requests once or infinitely in a loop. It can pause or
// reset the scenario. It can replay the scenario with a concurrency level of choice.
// The player accepts custom request definitions besides or instead of the access log
// input.
//
// The player is provided as an embeddable library, and a simple command is provided as
// subpackage.
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

// Player replays HTTP requests explicitly specified and/or read from an Apache access log.
type Player struct {
	options      Options
	accessLog    *reader
	requests     []Request
	position     int
	client       *http.Client
	errors       int
	serverErrors int
}

var errServerStatus = errors.New("unexpected server status")

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

	return &Player{
		options:   o,
		accessLog: r,
		requests:  o.Requests,
		client:    &http.Client{Transport: &http.Transport{}},
	}, nil
}

func (p *Player) nextRequest() (Request, bool) {
	var r Request
	if p.accessLog == nil {
		if p.position >= len(p.requests) {
			return r, false
		}

		r = p.requests[p.position]
		p.position++
		return r, true
	}

	var err error
	r, err = p.accessLog.ReadRequest()
	if err != nil && err != io.EOF {
		p.options.Log.Warnln("error while reading access log:", err)

		p.errors++
		if p.checkHaltError() {
			return r, false
		}

		return p.nextRequest()
	}

	if err == io.EOF {
		p.accessLog = nil
		return p.nextRequest()
	}

	p.requests = append(
		p.requests[:p.position],
		append(
			[]Request{r},
			p.requests[p.position:]...,
		)...,
	)

	p.position++
	return r, true
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

func (p *Player) checkHaltError() bool {
	if p.errors < p.options.HaltThreshold {
		return false
	}

	p.options.Log.Errorln("request errors exceeded threshold")

	switch p.options.HaltPolicy {
	case StopOnError, StopOn500:
		p.Stop()
		return true
	case PauseOnError, PauseOn500:
		p.Pause()
		return true
	}

	return false
}

func (p *Player) checkHaltStatus() bool {
	if p.serverErrors < p.options.HaltThreshold {
		return false
	}

	p.options.Log.Errorln("server errors exceeded threshold")

	switch p.options.HaltPolicy {
	case StopOn500:
		p.Stop()
		return true
	case PauseOn500:
		p.Pause()
		return true
	}

	return false
}

// Play replays the requests infinitely with the specified concurrency. If an access log is
// specified it reads it to the end only once, and it repeats the read requests from then on.
//
// When the player is currently playing requests, it is a noop.
func (p *Player) Play() {}

// Once replays the requests once.
//
// When the player is currently playing requests, it is a noop.
func (p *Player) Once() {
	for {
		r, ok := p.nextRequest()
		if !ok {
			break
		}

		err := p.sendRequest(r)
		if err == nil {
			continue
		}

		switch err {
		case errServerStatus:
			p.serverErrors++
			if p.checkHaltStatus() {
				break
			}
		default:
			p.errors++
			if p.checkHaltError() {
				break
			}
		}
	}
}

// Pause pauses the replay of the requests. When Play() or Once() are called after pause, the
// replay is resumed at the next request in order.
func (p *Player) Pause() {}

// Stop stops the replay of the requests. When Play() or Once() are called after stop, the
// replay is starts from the first request.
func (p *Player) Stop() {}
