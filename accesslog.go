package logreplay

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

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
	scanner    *bufio.Scanner
	lineParser Parser
	log        Logger
}

type defaultParser struct {
	format *regexp.Regexp
	names  []string
	log    Logger
}

func (p *defaultParser) Parse(l string) Request {
	var r Request
	m := p.format.FindStringSubmatch(l)
	for i, ni := range p.names {
		if i >= len(m) {
			break
		}

		switch ni {
		case "method":
			r.Method = m[i]
		case "host":
			r.Host = m[i]
		case "path":
			r.Path = m[i]
		}
	}

	return r
}

func newReader(input io.Reader, format string, p Parser, log Logger) (*reader, error) {
	if p == nil {
		rx := defaultFormat
		if format != "" {
			var err error
			rx, err = regexp.Compile(format)
			if err != nil {
				return nil, err
			}
		}

		p = &defaultParser{format: rx, names: rx.SubexpNames(), log: log}
	}

	return &reader{
		scanner:    bufio.NewScanner(input),
		lineParser: p,
		log:        log,
	}, nil
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

	req = r.lineParser.Parse(l)
	return
}
