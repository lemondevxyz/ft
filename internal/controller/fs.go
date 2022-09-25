package controller

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"

	"github.com/cespare/xxhash"
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
		path := path.Join(r.Name, v.Name())
		ret = append(ret, model.NewOsFileInfo(v, path, path))
	}

	val := &ReadDirValue{Files: ret}
	ctrl.Value(val)

	return val, nil

}

type VerifyValue struct {
	Same bool `json:"bool"`
}

func (f *FsController) Verify(rd io.Reader, ctrl model.Controller) error {
	val := &MoveData{}
	if err := DecodeOrFail(rd, ctrl, val); err != nil {
		return err
	}

	srcStat, err := f.fs.Stat(val.Src)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs-src-stat",
			Reason: err.Error(),
		})
		return err
	}

	dstStat, err := f.fs.Stat(val.Dst)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs-dst-stat",
			Reason: err.Error(),
		})
		return err
	}

	if srcStat.IsDir() || dstStat.IsDir() {
		err := fmt.Errorf("src and dst must not be directories")
		ctrl.Error(model.ControllerError{
			ID:     "fs-src-dst-directory",
			Reason: err.Error(),
		})

		return err
	}

	if srcStat.Size() != dstStat.Size() {
		err := fmt.Errorf("sizes do not match: %d, %d", srcStat.Size(), dstStat.Size())
		ctrl.Error(model.ControllerError{
			ID:     "fs-src-dst-size",
			Reason: err.Error(),
		})

		return err
	}

	var srcSum uint64
	srcChecksumFile := val.Src + ".xxh64"
	_, err = f.fs.Stat(srcChecksumFile)
	if err == nil {
		file, err := f.fs.OpenFile(srcChecksumFile, os.O_RDONLY, 0755)
		if err != nil {
			file.Close()
			ctrl.Error(model.ControllerError{
				ID:     "fs-src-hash-file",
				Reason: err.Error(),
			})

			return err
		}

		bites, err := ioutil.ReadAll(file)
		if err != nil {
			file.Close()
			ctrl.Error(model.ControllerError{
				ID:     "fs-src-hash-reading",
				Reason: err.Error(),
			})

			return err
		}

		num, err := strconv.ParseUint(string(bites), 16, 64)
		if err != nil {
			file.Close()
			ctrl.Error(model.ControllerError{
				ID:     "fs-src-hash-conversion",
				Reason: err.Error(),
			})

			return err
		}

		srcSum = num
	} else {
		srcFile, err := f.fs.OpenFile(val.Src, os.O_RDONLY, 0755)
		if err != nil {
			srcFile.Close()
			ctrl.Error(model.ControllerError{
				ID:     "fs-src-file",
				Reason: err.Error(),
			})

			return err
		}

		xxh64 := xxhash.New()
		io.Copy(xxh64, srcFile)
		srcFile.Close()
		srcSum = xxh64.Sum64()
		xxh64.Reset()
	}

	dstFile, err := f.fs.OpenFile(val.Dst, os.O_RDONLY, 0755)
	if err != nil {
		dstFile.Close()
		ctrl.Error(model.ControllerError{
			ID:     "fs-dst-file",
			Reason: err.Error(),
		})

		return err
	}
	xxh64 := xxhash.New()
	io.Copy(xxh64, dstFile)
	dstFile.Close()
	dstSum := xxh64.Sum64()

	if srcSum != dstSum {
		err := fmt.Errorf("sums do not match: %x - %x", srcSum, dstSum)
		ctrl.Error(model.ControllerError{
			ID:     "fs-src-dst-checksum-mismatch",
			Reason: err.Error(),
		})

		return err
	}

	ctrl.Value(val)
	return nil
}
