package logreplay

import "github.com/sirupsen/logrus"

// TODO: fill up the interface with the complete set of standard log functions

// Logger objects are used for logging events from the player.
type Logger interface {
	Errorln(...interface{})
	Warnln(...interface{})
	Infoln(...interface{})
	Debugln(...interface{})
	Debugf(string, ...interface{})
}

func enableDebugLog() { logrus.SetLevel(logrus.DebugLevel) }

func newDefaultLog() Logger {
	l := logrus.New()
	l.Level = logrus.GetLevel()
	return l
}
