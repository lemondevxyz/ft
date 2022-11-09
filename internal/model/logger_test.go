package model

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amoghe/distillog"
	"github.com/matryer/is"
)

func TestLoggerLock(t *testing.T) {
	l := &logger{}
	l.lock()

	is := is.New(t)
	is.Equal(false, l.mtx.TryLock())
}

func TestLoggerUnlock(t *testing.T) {
	l := &logger{}

	l.mtx.Lock()
	is := is.New(t)
	is.Equal(false, l.mtx.TryLock())

	l.unlock()
	is.Equal(true, l.mtx.TryLock())
}

func TestLoggerLogger(t *testing.T) {
	is := is.New(t)

	l := &logger{}
	log := distillog.NewNullLogger("")
	l.log = log

	is.Equal(log, l.logger())
}

func TestLoggerSetLogger(t *testing.T) {
	is := is.New(t)

	log := distillog.NewNullLogger("")

	l := &logger{}
	l.setLogger(log)

	is.Equal(log, l.log)
}

func TestLoggerClose(t *testing.T) {
	is := is.New(t)

	buf := &bufferCloser{bytes.NewBuffer(nil), false}

	l := distillog.NewStreamLogger("", buf)
	l.Close()

	is.True(buf.close)
}

func TestLoggerGeneric(t *testing.T) {
	is := is.New(t)

	buf := &bufferCloser{bytes.NewBuffer(nil), false}

	contains := func(str string) bool {
		defer buf.Reset()

		t.Log(buf.String())
		if strings.Contains(buf.String(), str) {
			return true
		}

		return false
	}

	l := &logger{log: distillog.NewStreamLogger("", buf)}
	l.Debugf("")
	is.True(contains("DEBUG"))

	l.Debugln("")
	is.True(strings.Contains(buf.String(), "\n"))
	is.True(contains("DEBUG"))

	l.Warningf("")
	is.True(contains("WARN"))

	l.Warningln("")
	is.True(strings.Contains(buf.String(), "\n"))
	is.True(contains("WARN"))

	l.Infof("")
	is.True(contains("INFO"))

	l.Infoln("")
	is.True(strings.Contains(buf.String(), "\n"))
	is.True(contains("INFO"))

	l.Errorf("")
	is.True(contains("ERROR"))

	l.Errorln("")
	is.True(strings.Contains(buf.String(), "\n"))
	is.True(contains("ERROR"))

	is.NoErr(l.Close())
	is.True(buf.close)
}
