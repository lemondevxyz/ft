package controller

import (
	"testing"

	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
)

func TestNewFsController(t *testing.T) {
	_, err := NewFsController(nil, nil)
	if err == nil {
		t.Fatalf("NewFsController(nil, nil): nil")
	}

	_, err = NewFsController(&Channel{}, nil)
	if err == nil {
		t.Fatalf("NewFsController(&Channel{}, nil): nil")
	}

	_, err = NewFsController(nil, afero.NewMemMapFs())
	if err == nil {
		t.Fatalf("NewFsController(nil, afero.NewMemMapFs()): nil")
	}

	_, err = NewFsController(&Channel{}, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}
}

func TestFsMkdir(t *testing.T) {
	fs := afero.NewMemMapFs()
	ctrl, err := NewFsController(&Channel{}, fs)
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}

	dc := &model.DummyController{}

	err = ctrl.MkdirAll(encodeJSON(MkdirAllData{Name: "asd"}), dc)
	if err != nil {
		t.Fatalf("ctrl.MkdirAll: %s", err.Error())
	}

	stat, err := fs.Stat("asd")
	if err != nil {
		t.Fatalf("fs.Stat: %s", err.Error())
	}

	if !stat.Mode().IsDir() {
		t.Fatalf("!stat.IsDir")
	}
}

func TestFsRemoveAll(t *testing.T) {

	fs := afero.NewMemMapFs()
	err := fs.MkdirAll("dir1/dir2", 0755)
	if err != nil {
		t.Fatalf("fs.MkdirAll: %s", err.Error())
	}

	ctrl, err := NewFsController(&Channel{}, fs)
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}

	dc := &model.DummyController{}
	err = ctrl.RemoveAll(encodeJSON(RemoveAllData{Name: "dir1"}), dc)
	if err != nil {
		t.Fatalf("ctrl.MkdirAll: %s", err.Error())
	}

	_, err = fs.Stat("dir1")
	if err == nil {
		t.Fatalf("fs.Stat: nil")
	}

	_, err = fs.Stat("dir1/dir2")
	if err == nil {
		t.Fatalf("fs.Stat: nil")
	}
}

func TestFsReadDir(t *testing.T) {
	fs := afero.NewMemMapFs()
	err := fs.MkdirAll("dir1/dir2", 0755)
	if err != nil {
		t.Fatalf("fs.MkdirAll: %s", err.Error())
	}

	fs.Create("dir1/dir2/f1.txt")
	fs.Create("dir1/dir2/f2.txt")

	ctrl, err := NewFsController(&Channel{}, fs)
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}

	dc := &model.DummyController{}
	fis, err := ctrl.ReadDir(encodeJSON(ReadDirData{"dir1"}), dc)
	if err != nil {
		t.Fatalf("ctrl.ReadDir: %s", err.Error())
	}

	if len(fis.Files) != 1 {
		t.Fatalf("wrong amount of file entries: have: %d - want: 1", len(fis.Files))
	}

	fis, err = ctrl.ReadDir(encodeJSON(ReadDirData{"dir1/dir2"}), dc)
	if err != nil {
		t.Fatalf("ctrl.ReadDir: %s", err.Error())
	}

	if len(fis.Files) != 2 {
		t.Fatalf("wrong amount of file entries: have: %d - want: 2", len(fis.Files))
	}
}

func TestFsMove(t *testing.T) {
	fs := afero.NewMemMapFs()
	err := fs.MkdirAll("old", 0755)
	if err != nil {
		t.Fatalf("fs.MkdirAll: %s", err.Error())
	}

	ctrl, err := NewFsController(&Channel{}, fs)
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}

	dc := &model.DummyController{}
	err = ctrl.Move(encodeJSON(MoveData{"old", "new"}), dc)
	if err != nil {
		t.Fatalf("ctrl.Move: %s", err.Error())
	}

	_, err = fs.Stat("new")
	if err != nil {
		t.Fatalf("fs.Stat: %s", err.Error())
	}

	_, err = fs.Stat("old")
	if err == nil {
		t.Fatalf("ft.Stat: <nil>")
	}
}
