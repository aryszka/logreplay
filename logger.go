package logreplay

import (
	"fmt"
	"github.com/sirupsen/logrus"
)

// TODO: fill up the interface with the complete set of standard log functions

// Logger objects are used for logging events from the player.
type Logger interface {
	Errorln(...interface{})
	Warnln(...interface{})
	Infoln(...interface{})
	Debugln(...interface{})
	Debugf(string, ...interface{})
}

type recorder struct {
	logs [][]interface{}
}

func enableDebugLog() { logrus.SetLevel(logrus.DebugLevel) }

func newDefaultLog() Logger {
	l := logrus.New()
	l.Level = logrus.GetLevel()
	return l
}

func (r *recorder) log(a ...interface{}) {
	r.logs = append(r.logs, a)
	println("logging", len(r.logs), len(a), len(r.logs[len(r.logs)-1]))
}

func (r *recorder) logf(l logrus.Level, f string, a ...interface{}) {
	r.log(l, fmt.Sprintf(f, a...))
}

func (r *recorder) Errorln(a ...interface{}) {
	r.log(append([]interface{}{logrus.ErrorLevel}, a...)...)
}

func (r *recorder) Warnln(a ...interface{}) {
	r.log(append([]interface{}{logrus.WarnLevel}, a...)...)
}

func (r *recorder) Infoln(a ...interface{}) {
	r.log(append([]interface{}{logrus.InfoLevel}, a...)...)
}

func (r *recorder) Debugln(a ...interface{}) {
	r.log(append([]interface{}{logrus.DebugLevel}, a...)...)
}

func (r *recorder) Debugf(f string, a ...interface{}) {
	r.logf(logrus.DebugLevel, f, a...)
}
