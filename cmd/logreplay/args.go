package main

import (
	"github.com/aryszka/logreplay"
	"flag"
	"errors"
	"log"
)

var (
	options logreplay.Options
	redirectBehavior string
	once bool
	errInvalidRedirectBehavior = errors.New("invalid redirect behavior")
)

func init() {
	flag.StringVar(
		&options.AccessLogFormat,
		"log-format",
		"",
		"a regexp for parsing the log entries, defaults to Apache2 Combined log format with Skipper extensions (Duration and Host)",
	)

	flag.StringVar(
		&options.Server,
		"server",
		"",
		"the HTTP network address to send the requests to. If not specified, it is taken from the request definitions, or defaults to localhost",
	)

	flag.StringVar(
		&options.DefaultScheme,
		"default-scheme",
		"http",
		"http scheme to be used when otherwise not inferrable from the server option or the log entry",
	)

	flag.IntVar(
		&options.ConcurrentSessions,
		"concurrent-sessions",
		1,
		"number of concurrent sessions to run",
	)

	flag.StringVar(
		&redirectBehavior,
		"redirect-behavior",
		"nofollow",
		"behavior applied when a redirect response was received from the server (nofollow, samehost, follow)",
	)

	flag.IntVar(
		&options.PostContentLength,
		"post-content-length",
		0,
		"content length to be sent with P* requests",
	)

	flag.Float64Var(
		&options.PostContentLengthDeviation,
		"post-content-length-deviation",
		0,
		"variance in content length to be sent with P* requests",
	)

	flag.BoolVar(
		&options.PostSetContentLength,
		"post-set-content-length",
		false,
		"indicates whether the HTTP Content-Length header should be set",
	)

	flag.BoolVar(
		&options.HaltOn500,
		"halt-on-500",
		false,
		"inidicates whether the replay should halt on 5xx errors or only on client errors",
	)

	flag.IntVar(
		&options.HaltThreshold,
		"halt-threshold",
		logreplay.DefaultHaltThreshold,
		"the limit that continuous failures need to reach to make the player halt",
	)

	flag.Float64Var(
		&options.Throttle,
		"throttle",
		0,
		"maximum outgoing overall request per second rate",
	)

	flag.BoolVar(
		&once,
		"once",
		false,
		"tells the player to replay the input scenario only once and exit",
	)

	flag.Parse()

	switch redirectBehavior {
	case "nofollow":
		options.RedirectBehavior = logreplay.NoFollow
	case "samehost":
		options.RedirectBehavior = logreplay.FollowSameHost
	case "follow":
		options.RedirectBehavior = logreplay.FollowRedirect
	default:
		flag.PrintDefaults()
		log.Fatal(errInvalidRedirectBehavior)
	}
}
