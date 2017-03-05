// Package logreplay provides a client that can replay HTTP requests based on access logs.
//
// The player can replay a scenario only once, or infinitely in a loop. It can
// pause or reset. It can replay the scenario with a concurrency level of
// choice. The player accepts custom request definitions besides or instead of the access
// log input. It supports custom format for the built-in parser, or a complete custom
// parser. It gives control over the target network address by allowing to use a proxy
// server or making requests directly to the hosts defined in the access log. Other
// features: control over the redirect behavior, sending varying content with POST/PUT/
// PATCH requests, controlling error behavior, artificially throttling request rate. For
// details about controlling the replay, see the documentation of the Options type.
//
// The player is provided as an embeddable library, extended with a simple command line
// wrapper.
package logreplay

import (
	"errors"
	"io"
)

// RedirectBehavior defines how to handle redirect responses.
type RedirectBehavior int

const (

	// NoFollow tells the player not to follow redirects.
	NoFollow RedirectBehavior = iota

	// FollowSameHost tells the player to follow redirects only to the same host
	// as the request was made to.
	FollowSameHost

	// FollowRedirect tells the player to follow all redirects. (Not recommended
	// during load tests.)
	FollowRedirect
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
	// The HTTP ContentLength header is set only when SetContentLength is false.
	ContentLength int

	// ContentLengthDeviation defines how much the actual random content length of
	// a request can differ from ContentLength.
	ContentLengthDeviation float64

	// SetContentLength defines if the request content should be sent with defined
	// Content-Length header.
	SetContentLength bool
}

// Parser can parse a log entry.
type Parser interface {

	// Parse parses a log entry. It accepts a log line as a string
	// and returns a Request definition.
	Parse(string) *Request
}

// Options is used to initialize a player.
type Options struct {

	// Requests to be executed by the player in the specified order.
	//
	// When used together with AccessLog, requests defined in this field are
	// executed after the requests read from the AccessLog.
	Requests []*Request

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

	// Server is a network address to send the requests to.
	Server string

	// DefaultScheme tells whether http or https should be used when the network address
	// is taken from the host specified in the request, and the scheme is not specified.
	DefaultScheme string

	// ConcurrentSessions tells the player how many concurrent clients should replay
	// the requests.
	//
	// Defaults to 1.
	ConcurrentSessions int

	// RedirectBehavior tells the player how to act on redirect responses.
	RedirectBehavior RedirectBehavior

	// PostContentLength tells the player the average request content size to send in
	// case of POST, PUT and PATCH requests ware taken from the access log.
	PostContentLength int

	// PostContentLengthDeviation defines how much the actual random content length of
	// a request taken from the access log can differ from PostContentLength.
	PostContentLengthDeviation float64

	// PostSetContentLength defines whether a request content should be sent with defined
	// Content-Length header.
	PostSetContentLength bool

	// Log defines a custom logger for the player.
	Log Logger

	// HaltOn500 tells the player to stop not only on errors but on server errors, too.
	HaltOn500 bool

	// HaltThreshold tells the player after how many errors or 500s it should stop.
	// Default: 128.
	HaltThreshold int

	// Throttle maximizes the outgoing overall request per second rate.
	Throttle float64
}

type (
	signalToken    struct{}
	signalChannel  chan signalToken
	errorChannel   chan error
	requestChannel chan *Request
)

// Player replays HTTP requests explicitly specified and/or read from an Apache access log.
type Player struct {
	options        Options
	accessLog      *reader
	logEntries     []*Request
	customRequests []*Request
	errors         int
	serverErrors   int
	players        []*player
	once           bool
	waitingError   []errorChannel
	notRunning     signalChannel
	signalPlay     chan errorChannel
	signalOnce     chan errorChannel
	signalPause    chan signalChannel
	signalStop     chan signalChannel
}

var (
	// ErrServerError is returned when the server responded with 5xx multiple times in
	// a row.
	ErrServerError = errors.New("server error")

	// ErrRequestError is returned when the request failed multiple times in a row.
	ErrRequestError = errors.New("request failed")

	// ErrNoRequests is returned when the there are no requests to be executed by Play().
	ErrNoRequests = errors.New("no requests to play")
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

	if o.DefaultScheme == "" {
		o.DefaultScheme = "http"
	}

	if o.ConcurrentSessions <= 0 {
		o.ConcurrentSessions = 1
	}

	// enable starting the player:
	notRunning := make(signalChannel, 1)
	notRunning <- signalToken{}

	return &Player{
		options:        o,
		accessLog:      r,
		customRequests: o.Requests,
		notRunning:     notRunning,
		signalPlay:     make(chan errorChannel, 1),
		signalOnce:     make(chan errorChannel, 1),
		signalPause:    make(chan signalChannel, 1),
		signalStop:     make(chan signalChannel, 1),
	}, nil
}

func (p *Player) contentSettings(r *Request) {
	switch r.Method {
	case "POST", "PUT", "PATCH":
	default:
		return
	}

	r.ContentLength = p.options.PostContentLength
	r.ContentLengthDeviation = p.options.PostContentLengthDeviation
	r.SetContentLength = p.options.PostSetContentLength
}

func (p *Player) nextRequest(position int) (*Request, error) {
	if position < len(p.logEntries) {
		r := p.logEntries[position]
		p.contentSettings(r)
		return r, nil
	}

	if p.accessLog == nil {
		position -= len(p.logEntries)
		if position >= len(p.customRequests) {
			return nil, io.EOF
		}

		return p.customRequests[position], nil
	}

	var err error
	r, err := p.accessLog.ReadRequest()
	if err != nil && err != io.EOF {
		p.options.Log.Warnln("error while reading access log:", err)
		return nil, err
	}

	if err == io.EOF {
		p.accessLog = nil
		return p.nextRequest(position)
	}

	p.logEntries = append(p.logEntries, r)
	p.contentSettings(r)
	return r, nil
}

func (p *Player) checkHaltError() bool {
	if p.errors < p.options.HaltThreshold {
		return false
	}

	p.options.Log.Errorln("request errors exceeded threshold")
	return true
}

func (p *Player) checkHaltStatus() bool {
	if !p.options.HaltOn500 || p.serverErrors < p.options.HaltThreshold {
		return false
	}

	p.options.Log.Errorln("server errors exceeded threshold")
	return true
}

func (p *Player) checkHalt(err error) bool {
	err = p.checkError(err)
	if err == nil {
		return false
	}

	p.stop(err)
	return true
}

func (p *Player) checkError(err error) error {
	switch err {
	case nil:
		p.errors = 0
		p.serverErrors = 0
	case ErrNoRequests:
		return ErrNoRequests
	case ErrServerError:
		p.serverErrors++
		if p.checkHaltStatus() {
			return ErrServerError
		}
	default:
		p.errors++
		if p.checkHaltError() {
			return ErrRequestError
		}
	}

	return nil
}

func (p *Player) stopPlayer(i int, feed requestChannel) {
	if i < 0 {
		for i = 0; i < len(p.players); i++ {
			if p.players[i].feed == feed {
				break
			}
		}
	}

	if i >= len(p.players) {
		return
	}

	close(p.players[i].feed)
	p.players = append(p.players[:i], p.players[i+1:]...)
}

func (p *Player) stop(err error) {
	for i := range p.players {
		p.stopPlayer(i, nil)
	}

	err = p.checkError(err)
	for _, w := range p.waitingError {
		w <- err
	}

	p.notRunning <- signalToken{}
}

func (p *Player) feedRequest(f feedRequest) bool {
	r, err := p.nextRequest(f.position)
	if err == io.EOF {
		if p.once {
			p.stopPlayer(-1, f.response)
			if len(p.players) == 0 {
				p.stop(nil)
				return false
			}

			return true
		}

		if f.position == 0 {
			p.stop(ErrNoRequests)
			return false
		}

		f.response <- nil
		return true
	}

	if err != nil {
		if p.checkHalt(err) {
			return false
		}

		return true
	}

	var rc Request
	rc = *r
	f.response <- &rc
	return true
}

func (p *Player) run() {
	requestFeed := make(chan feedRequest)
	results := make(errorChannel)
	p.waitingError = nil

	o := p.options
	o.Throttle = p.options.Throttle / float64(p.options.ConcurrentSessions)
	p.players = make([]*player, p.options.ConcurrentSessions)
	for i := 0; i < p.options.ConcurrentSessions; i++ {
		p.players[i] = newPlayer(o, requestFeed, results)
		go p.players[i].run()
	}

	var feed chan feedRequest
	for {
		select {
		case d := <-p.signalPlay:
			p.waitingError = append(p.waitingError, d)
			p.once = false
			feed = requestFeed
		case d := <-p.signalOnce:
			p.waitingError = append(p.waitingError, d)
			p.once = true
			feed = requestFeed
		case d := <-p.signalPause:
			feed = nil
			close(d)
		case d := <-p.signalStop:
			p.stop(nil)
			close(d)
			return
		case err := <-results:
			if p.checkHalt(err) {
				return
			}
		case f := <-feed:
			if !p.feedRequest(f) {
				return
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

func (p *Player) signalError(s chan errorChannel) error {
	done := make(errorChannel)
	s <- done
	return <-done
}

// Play replays the requests infinitely with the specified concurrency. If an access log is
// specified it reads it to the end only once, and it repeats the read requests from then on.
//
// When the player is currently playing requests, it is a noop. Play is blocking, in order to
// use Pause() or Stop(), they need to be called from a different goroutine. To cleanup
// resources, it must be stopped. Play(), Once() and Pause() can be called any number of times
// during a session started by Play() or Once().
func (p *Player) Play() error {
	if !p.isRunning() {
		go p.run()
	}

	return p.signalError(p.signalPlay)
}

// Once replays the requests once.
//
// When the player is currently playing requests, it is a noop. Once is blocking, in order to
// use Pause() or Stop(), they need to be called from a different goroutine. To cleanup
// resources, it must run to the end, or it must be stopped. Play(), Once() and Pause() can be
// called any number of times during a session started by Play() or Once().
func (p *Player) Once() error {
	if !p.isRunning() {
		go p.run()
	}

	return p.signalError(p.signalOnce)
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
