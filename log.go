package logreplay

import "github.com/sirupsen/logrus"

// Logger objects are used for logging events from the player.
type Logger interface {
	Errorln(...interface{})
	Warnln(...interface{})
	Debugln(...interface{})
}

func enableDebugLog() { logrus.SetLevel(logrus.DebugLevel) }

func newDefaultLog() Logger {
	l := logrus.New()
	l.Level = logrus.GetLevel()
	return l
}
