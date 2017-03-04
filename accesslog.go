package logreplay

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// known bugs:
// - escaped double quotes in the HTTP message
// - whitespace handling in the HTTP message

const defaultFormatExpression = `^` +

	// remote address:
	`(([0-9.]+(\s*,\s*[0-9.]+)*)|-)\s*` +

	// client identity:
	`([a-zA-Z0-9_.]+|-)\s*` +

	// user id:
	`([a-zA-Z0-9_.]+|-)\s*` +

	// time:
	`([[]([^]])*[]]|-)\s*` +

	// message:
	`"(?P<method>[^ ^"]+)\s+(?P<path>[^ ^"]+)\s+([^ ^"]+)"\s*` +

	// status:
	`([0-9]+)\s*` +

	// response size:
	`([0-9]+)\s*` +

	// referrer (the comma must be a mistake):
	`("([^"]+)",?\s*)?` +

	// user agent:
	`("([^"]+)"\s*)?` +

	// duration:
	`(([0-9]+)\s*)?` +

	// host:
	`((?P<host>\S+)\s*)?` +

	`$`

var (
	defaultFormat = regexp.MustCompile(defaultFormatExpression)
	defaultNames  = defaultFormat.SubexpNames()
)

type reader struct {
	scanner *bufio.Scanner
	log     Logger
}

func newReader(input io.Reader, log Logger) *reader {
	return &reader{
		scanner: bufio.NewScanner(input),
		log:     log,
	}
}

// document default token size
func (r *reader) ReadRequest() (req Request, err error) {
	if !r.scanner.Scan() {
		if err = r.scanner.Err(); err != nil {
			return
		}

		err = io.EOF
		return
	}

	l := r.scanner.Text()
	l = strings.TrimSpace(l)
	if l == "" {
		return r.ReadRequest()
	}

	r.log.Debugf("matching: >%s<", r.scanner.Text())
	m := defaultFormat.FindStringSubmatch(l)
	for i, mi := range m {
		r.log.Debugf("submatch: %d: >%s<", i, mi)
	}

	for i, ni := range defaultNames {
		if i >= len(m) {
			break
		}

		switch ni {
		case "method":
			req.Method = m[i]
		case "host":
			req.Host = m[i]
		case "path":
			req.Path = m[i]
		}
	}

	r.log.Debugln("scanned:", r.scanner.Text())
	return
}
