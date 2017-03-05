package logreplay

import (
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type client struct {
	options    Options
	httpClient *http.Client
}

func newClient(o Options) *client {
	c := &client{options: o}
	c.httpClient = &http.Client{
		Transport:     &http.Transport{},
		CheckRedirect: c.checkRedirect,
	}

	return c
}

func (c *client) checkRedirect(rn *http.Request, rp []*http.Request) error {
	switch c.options.RedirectBehavior {
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

func (c *client) createHTTPRequest(r *Request) (*http.Request, error) {
	m := r.Method
	if m == "" {
		m = "GET"
	}

	a := c.options.Server
	if a == "" {
		if r.Host == "" {
			a = "localhost"
		} else {
			a = r.Host
		}

		if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
			a = c.options.DefaultScheme + "://" + a
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
		h = c.options.Server
	}

	if h == "" {
		h = "localhost"
	}

	hr.Host = h

	return hr, nil
}

func (c *client) do(r *Request) error {
	hr, err := c.createHTTPRequest(r)
	if err != nil {
		c.options.Log.Errorln("failed to create request", err)
		return err
	}

	rsp, err := c.httpClient.Do(hr)
	if err != nil {
		c.options.Log.Warnln("error while making request:", err)
		return err
	}

	defer rsp.Body.Close()

	if rsp.StatusCode >= http.StatusInternalServerError {
		return ErrServerError
	}

	_, err = ioutil.ReadAll(rsp.Body)
	if err != nil {
		c.options.Log.Warnln("error while reading request body:", err)
		return err
	}

	return nil
}
