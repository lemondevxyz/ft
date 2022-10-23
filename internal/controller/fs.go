package controller

import (
	"fmt"
	"io"
	"io/fs"
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

// FsGenericData represents the model for most fs operations
type FsGenericData struct {
	Name string `json:"name" example:"/home/tim/file.txt" description:"A path to a file or a directory"`
}

type RemoveAllData FsGenericData
type RemoveAllValue FsGenericData

// @Title Removes the file or directory and its sub-directories
// @Description Removes the file or directory and its sub-directories
// @Param   removeAllData body FsGenericData true "The directory's path"
// @Success 200 object FsGenericData         "FsGenericData JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/remove [post]
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

// @Title Creates a new directory
// @Description Creates a new directory and its subdirectories if needed
// @Param   mkdirAllData body FsGenericData true "The directory's path"
// @Success 200 object FsGenericData         "FsGenericData JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/mkdir [post]
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
	Src string `json:"src" example:"/home/tim/src-file.txt" description:"The source file you want to rename or move"`
	Dst string `json:"dst" example:"/home/tim/dst-file.txt" description:"The destination you want to move the src file to"`
}

type MoveValue MoveData

// @Title Move a file or directory
// @Description Moves a file or directory into a new one, or renames the file
// @Param   moveData body FsGenericData true      "The source and destination in the form of MoveData"
// @Success 200 object FsGenericData             "FsGenericData JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/move [post]
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
	Files []model.OsFileInfo `json:"files" example:"[{\"absPath\":\"/home\",\"modTime\":\"2021-10-24T05:30:08.691024236+08:00\",\"mode\":2147484141,\"name\":\"home\",\"path\":\"/home\",\"size\":4096}]" description:"An array of model.OsFileInfo"`
}

// @Title Reads a directory
// @Description Reads all the files in a directory and returns them.
// @Param   readDirData body FsGenericData     true "The path of the file"
// @Success 200 object ReadDirValue          "ReadDirValue JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/readdir [post]
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

		file := model.NewOsFileInfo(v, path, path)
		if file.Mode()&fs.ModeSymlink != 0 {
			rd, ok := f.fs.(afero.LinkReader)
			if ok {
				absPath, err := rd.ReadlinkIfPossible(path)
				if err == nil {
					stat, err := f.fs.Stat(absPath)
					if err == nil {
						file.AbsolutePath = absPath

						val := stat.Mode()
						file.FakeMode = &val
					}
				}
			}
		}

		ret = append(ret, file)
	}

	val := &ReadDirValue{Files: ret}
	ctrl.Value(val)

	return val, nil

}

type SizeData FsGenericData
type SizeValue struct {
	Size int64 `json:"size" example:"1024" description:"Size in bytes"`
}

// @Title Calculate the size of a directory
// @Description Reads all the files in a directory and adds them together then returns the sum.
// @Param   sizeData   body FsGenericData true    "The path of the file"
// @Success 200 object FsGenericData              "FsGenericData JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/size [post]
func (f *FsController) Size(rd io.Reader, ctrl model.Controller) (int64, error) {
	val := &SizeData{}
	if err := DecodeOrFail(rd, ctrl, val); err != nil {
		return -1, err
	}

	stat, err := f.fs.Stat(val.Name)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs-stat",
			Reason: err.Error(),
		})
		return -1, err
	}

	if !stat.Mode().IsDir() {
		ctrl.Value(SizeValue{
			Size: stat.Size(),
		})
		return stat.Size(), nil
	}

	var size int64 = 0
	err = afero.Walk(f.fs, val.Name, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		size += info.Size()

		return nil
	})
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "fs-walk",
			Reason: err.Error(),
		})
		return -1, err
	}

	ctrl.Value(SizeValue{
		Size: size,
	})

	return size, nil

}

type VerifyValue struct {
	Same bool `json:"same" example:"false" description:"If same is true, the two files are identical. You probably won't use this because the request returns an error if they files are not identical"`
}

// @Title Verify two files
// @Description Check if two files contain the same content or not. This function uses the xxhash algorithm and if there's a file with the extension "xxh64", it uses that instead of reading the file and computing the hash.
// @Param   moveData   body MoveData true    "The two files in the structure of MoveData"
// @Success 200 object VerifyValue           "MoveData JSON"
// @Failure 400 object model.ControllerError "model.ControllerError JSON"
// @Resource file system routes
// @Route /api/v0/fs/verify [post]
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
