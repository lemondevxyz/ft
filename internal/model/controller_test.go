package model

import (
	"bytes"
	"io"
	"testing"

	"github.com/matryer/is"
)

func TestDummyControllerError(t *testing.T) {
	is := is.New(t)

	ctrl := &DummyController{}
	is.True(ctrl.err == nil)

	err := ControllerError{
		ID: "asd",
	}
	ctrl.Error(err)

	is.Equal(ctrl.err, &err)
}

func TestDummyControllerValue(t *testing.T) {
	is := is.New(t)

	ctrl := &DummyController{}

	ctrl.err = &ControllerError{}
	is.True(ctrl.Value(nil) != nil)
	ctrl.err = nil

	is.NoErr(ctrl.Value("asd"))

	is.True(ctrl.buf != nil)
	is.True(ctrl.enc != nil)
	is.Equal(ctrl.buf.String(), "\"asd\"\n")
}

func TestDummyControllerGetError(t *testing.T) {
	is := is.New(t)
	ctrl := &DummyController{}

	is.Equal(ctrl.GetError(), nil)

	err := &ControllerError{}
	ctrl.err = err

	is.Equal(ctrl.GetError(), err)
}

func TestDummyControllerRead(t *testing.T) {
	is := is.New(t)

	ctrl := &DummyController{}
	err := ctrl.Read(nil)

	is.Equal(err, io.EOF)

	is.True(ctrl.buf != nil)
	is.True(ctrl.dec != nil)

	ctrl.buf = bytes.NewBuffer([]byte("\"asd\"\n"))
	s := ""

	ctrl.dec = nil

	is.NoErr(ctrl.Read(&s))
	is.Equal(s, "asd")
}
