package logreplay

import (
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type requestRequest struct {
	position int
	response requestChannel
}

type player struct {
	options      Options
	sendRequests chan requestRequest
	sendErrors   errorChannel
	receive      requestChannel
	position     int
	client       *http.Client
}

func newPlayer(o Options, sendRequests chan requestRequest, sendErrors errorChannel) *player {
	p := &player{
		options:      o,
		sendRequests: sendRequests,
		sendErrors:   sendErrors,
		receive:      make(requestChannel),
	}

	p.client = &http.Client{
		Transport:     &http.Transport{},
		CheckRedirect: p.checkRedirect,
	}

	return p
}

func (p *player) checkRedirect(rn *http.Request, rp []*http.Request) error {
	switch p.options.RedirectBehavior {
	case FollowSameHost:
		if rn.URL.Host != rp[0].URL.Host {
			return http.ErrUseLastResponse
		}

		return nil
	case FollowRedirect:
		return nil
	default:
		return http.ErrUseLastResponse
	}
}

func (p *player) createHTTPRequest(r *Request) (*http.Request, error) {
	m := r.Method
	if m == "" {
		m = "GET"
	}

	a := p.options.Server
	if a == "" {
		if r.Host == "" {
			a = "http://localhost"
		} else {
			a = r.Host
		}

		if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
			a = p.options.DefaultScheme + "://" + a
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

	hasContent := r.ContentLength > 0 || r.ContentLengthDeviation > 0
	var (
		body          io.ReadCloser
		contentLength int
	)

	if hasContent {
		contentLength = deviateMin(r.ContentLength, r.ContentLengthDeviation)
		body = ioutil.NopCloser(randomText(contentLength))
	}

	hr, err := http.NewRequest(m, u.String(), body)
	if err != nil {
		return nil, err
	}

	if hasContent && r.SetContentLength {
		hr.ContentLength = int64(contentLength)
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

func (p *player) sendRequest(r *Request) error {
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
		return ErrServerError
	}

	_, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		p.options.Log.Warnln("error while reading request body:", err)
		return err
	}

	return nil
}

func (p *player) run() {
	for {
		select {
		case p.sendRequests <- requestRequest{
			position: p.position,
			response: p.receive,
		}:
		case r, open := <-p.receive:
			if !open {
				return
			}

			if r == nil {
				p.position = 0
				continue
			}

			p.position++
			err := p.sendRequest(r)
			p.sendErrors <- err
		}
	}
}
