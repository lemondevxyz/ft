package model

import (
	"sync"

	"github.com/amoghe/distillog"
)

type logger struct {
	mtx sync.Mutex
	log distillog.Logger
}

func (l *logger) lock()   { l.mtx.Lock() }
func (l *logger) unlock() { l.mtx.Unlock() }

func (l *logger) logger() distillog.Logger {
	l.lock()
	defer l.unlock()
	return l.log
}

func (l *logger) setLogger(log distillog.Logger) {
	l.lock()
	l.log = log
	l.unlock()
}

func (l *logger) Debugf(format string, v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Debugf(format, v...)
	}
	l.unlock()
}

func (l *logger) Debugln(v ...interface{}) {
	l.lock()
	if l.log != nil {

		l.log.Debugln(v...)
	}
	l.unlock()
}

func (l *logger) Infof(format string, v ...interface{}) {
	l.lock()
	if l.log != nil {

		l.log.Infof(format, v...)
	}
	l.unlock()
}

func (l *logger) Infoln(v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Infoln(v...)
	}
	l.unlock()
}

func (l *logger) Warningf(format string, v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Warningf(format, v...)
	}
	l.unlock()
}

func (l *logger) Warningln(v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Warningln(v...)
	}
	l.unlock()
}

func (l *logger) Errorf(format string, v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Errorf(format, v...)
	}
	l.unlock()
}

func (l *logger) Errorln(v ...interface{}) {
	l.lock()
	if l.log != nil {
		l.log.Errorln(v...)
	}
	l.unlock()
}

func (l *logger) Close() error {
	l.lock()
	defer l.unlock()
	return l.log.Close()
}
