package controller

import (
	"fmt"
	"io"

	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
)

type FsController struct {
	fs afero.Fs
	ch *Channel
}

func NewFsController(ch *Channel, f afero.Fs) (*FsController, error) {
	if ch == nil || f == nil {
		return nil, fmt.Errorf("all parameters are required.")
	}

	return &FsController{
		fs: f,
		ch: ch,
	}, nil
}

type FsGenericData struct {
	Name string `json:"name"`
}

type RemoveAllData FsGenericData
type RemoveAllValue FsGenericData

func (f *FsController) RemoveAll(rd io.Reader, ctrl model.Controller) error {
	r := &RemoveAllData{}
	if err := DecodeOrFail(rd, ctrl, r); err != nil {
		return err
	}

	err := f.fs.RemoveAll(r.Name)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs",
			Reason: err.Error(),
		})
		return err
	}

	f.ch.Announce(EventFsRemove(r.Name))

	ctrl.Value(r)
	return nil
}

type MkdirAllData FsGenericData
type MkdirAllValue FsGenericData

func (f *FsController) MkdirAll(rd io.Reader, ctrl model.Controller) error {
	r := &MkdirAllData{}
	if err := DecodeOrFail(rd, ctrl, r); err != nil {
		return err
	}

	err := f.fs.MkdirAll(r.Name, 0755)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs",
			Reason: err.Error(),
		})
		return err
	}

	ctrl.Value(r)
	f.ch.Announce(EventFsMkdir(r.Name))

	return nil
}

type MoveData struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

type MoveValue MoveData

func (f *FsController) Move(rd io.Reader, ctrl model.Controller) error {
	r := &MoveData{}
	if err := DecodeOrFail(rd, ctrl, r); err != nil {
		return err
	}

	err := f.fs.Rename(r.Src, r.Dst)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs",
			Reason: err.Error(),
		})
		return err
	}

	f.ch.Announce(EventFsMove(r.Src, r.Dst))

	ctrl.Value(r)
	return nil
}

type ReadDirData FsGenericData
type ReadDirValue struct {
	Files []model.OsFileInfo `json:"files"`
}

func (f *FsController) ReadDir(rd io.Reader, ctrl model.Controller) (*ReadDirValue, error) {
	r := &ReadDirData{}
	if err := DecodeOrFail(rd, ctrl, r); err != nil {
		return nil, err
	}

	fis, err := afero.ReadDir(f.fs, r.Name)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs-readdir",
			Reason: err.Error(),
		})

		return nil, err
	}

	ret := []model.OsFileInfo{}
	for _, v := range fis {
		ret = append(ret, model.NewOsFileInfo(v))
	}

	val := &ReadDirValue{Files: ret}
	ctrl.Value(val)

	return val, nil

}
