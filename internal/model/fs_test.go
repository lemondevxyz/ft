package model

import (
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/matryer/is"
)

type mock struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	path    string
}

func (m mock) Name() string       { return m.name }
func (m mock) Size() int64        { return m.size }
func (m mock) Mode() fs.FileMode  { return m.mode }
func (m mock) ModTime() time.Time { return m.modTime }
func (m mock) IsDir() bool        { return m.mode.IsDir() }
func (m mock) Sys() interface{}   { return nil }

func TestNewOsFileInfo(t *testing.T) {
	is := is.New(t)

	m := mock{}
	is.Equal(NewOsFileInfo(m, "path", "abs"), OsFileInfo{m, "path", "abs", nil})
}

func TestOsFileInfoMap(t *testing.T) {
	m := mock{}
	fi := OsFileInfo{m, "path", "abs", nil}

	is := is.New(t)

	mapstring := fi.Map()
	is.Equal(mapstring["name"], fi.Name())
	is.Equal(mapstring["size"], fi.Size())
	is.Equal(mapstring["mode"], fi.Mode())
	is.Equal(mapstring["modTime"], fi.ModTime())
	is.Equal(mapstring["path"], fi.Path)
	is.Equal(mapstring["absPath"], fi.AbsolutePath)

	fm := fs.FileMode(32)
	fi.FakeMode = &fm

	mapstring = fi.Map()
	is.Equal(mapstring["mode"], fm)
}

func TestOsFileInfoMarshalJSON(t *testing.T) {
	m := mock{}
	fi := OsFileInfo{m, "path", "abs", nil}

	is := is.New(t)

	bytes1, err := fi.MarshalJSON()
	is.NoErr(err)

	bytes2, err := json.Marshal(fi.Map())
	is.NoErr(err)

	is.Equal(bytes1, bytes2)
}
