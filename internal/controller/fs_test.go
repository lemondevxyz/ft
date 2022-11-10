package controller

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"testing"

	"github.com/cespare/xxhash"
	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/matryer/is"
	"github.com/spf13/afero"
)

func TestNewFsController(t *testing.T) {
	is := is.New(t)

	_, err := NewFsController(nil, nil)
	is.True(err != nil)

	_, err = NewFsController(&Channel{}, nil)
	is.True(err != nil)

	_, err = NewFsController(nil, afero.NewMemMapFs())
	is.True(err != nil)

	_, err = NewFsController(&Channel{}, afero.NewMemMapFs())
	is.NoErr(err)
}

func TestFsMkdirAll(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	ctrl, err := NewFsController(&Channel{}, fs)
	is.NoErr(err)

	dc := &model.DummyController{}

	is.Equal(ctrl.MkdirAll(&bytes.Buffer{}, dc), DecodeOrFail(&bytes.Buffer{}, dc, dc))

	ctrl.fs = afero.NewReadOnlyFs(fs)
	is.Equal(ctrl.MkdirAll(encodeJSON(is, MkdirAllData{Name: "asd"}), dc), ctrl.fs.MkdirAll("asd", 0755))

	ctrl.fs = fs

	_, ch := ctrl.ch.Subscribe()
	finish := make(chan sse.Event)
	go func() {
		val := <-ch
		finish <- val
	}()

	is.NoErr(ctrl.MkdirAll(encodeJSON(is, MkdirAllData{Name: "asd"}), dc))

	stat, err := fs.Stat("asd")
	is.NoErr(err)

	is.True(stat.Mode().IsDir())

	is.Equal(<-finish, EventFsMkdir("asd"))
}

func TestFsRemoveAll(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(fs.MkdirAll("dir1/dir2", 0755))

	ctrl, err := NewFsController(&Channel{}, fs)
	is.NoErr(err)

	dc := &model.DummyController{}

	is.Equal(ctrl.RemoveAll(&bytes.Buffer{}, dc), DecodeOrFail(&bytes.Buffer{}, dc, dc))

	ctrl.fs = afero.NewReadOnlyFs(fs)

	is.Equal(ctrl.RemoveAll(encodeJSON(is, RemoveAllData{Name: "dir1"}), dc), ctrl.fs.RemoveAll("dir1"))
	ctrl.fs = fs

	id, ch := ctrl.ch.Subscribe()
	finish := make(chan sse.Event)
	go func() {
		val := <-ch
		finish <- val
	}()

	is.NoErr(ctrl.RemoveAll(encodeJSON(is, RemoveAllData{Name: "dir1"}), dc))

	_, err = fs.Stat("dir1")
	is.True(err != nil)

	_, err = fs.Stat("dir1/dir2")
	is.True(err != nil)

	is.Equal(<-finish, EventFsRemove("dir1"))
	ctrl.ch.Unsubscribe(id)
}

type SymlinkFs struct {
	afero.Fs
	symLink map[string]string
}

func (s *SymlinkFs) SymlinkIfPossible(oldname, newname string) error {
	s.symLink[oldname] = newname

	return nil
}

func (s *SymlinkFs) ReadlinkIfPossible(name string) (string, error) {
	val, ok := s.symLink[name]
	if !ok {
		return "", io.EOF
	}

	return val, nil
}

func TestFsReadDir(t *testing.T) {
	is := is.New(t)

	afs := afero.NewMemMapFs()
	is.NoErr(afs.MkdirAll("dir1/dir2", 0755))

	_, err := afs.Create("dir1/dir2/f1.txt")
	is.NoErr(err)
	_, err = afs.Create("dir1/dir2/f2.txt")
	is.NoErr(err)

	ctrl, err := NewFsController(&Channel{}, afs)
	is.NoErr(err)

	dc := &model.DummyController{}
	is.Equal(onlyLast(ctrl.ReadDir(&bytes.Buffer{}, dc)).(error),
		DecodeOrFail(&bytes.Buffer{}, dc, dc))

	is.Equal(onlyLast(ctrl.ReadDir(encodeJSON(is, ReadDirData{"404"}), dc)).(error),
		onlyLast(afero.ReadDir(afs, "404")).(error))

	fis, err := ctrl.ReadDir(encodeJSON(is, ReadDirData{"dir1"}), dc)
	is.NoErr(err)

	is.Equal(len(fis.Files), 1)

	fis, err = ctrl.ReadDir(encodeJSON(is, ReadDirData{"dir1/dir2"}), dc)
	is.NoErr(err)

	is.Equal(len(fis.Files), 2)
}

type badReader struct{}

func (b *badReader) Read(a []byte) (n int, err error) {
	return 0, io.ErrClosedPipe
}

func TestReadAsHash(t *testing.T) {
	is := is.New(t)

	buf := bytes.NewBuffer([]byte("fbcea83c8a378bf1"))

	_, err := readAsHash(&badReader{})
	is.Equal(err, io.ErrClosedPipe)

	hash, err := readAsHash(buf)

	is.NoErr(err)
	is.Equal(uint64(18144624926692707313), hash)
}

func TestFileOrHashFile(t *testing.T) {
	is := is.New(t)

	afs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(afs, "./ok.txt", []byte("abcdef"), 0755))

	file, err := fileOrHashFile(afs, "./not-found.txt")
	is.True(err != nil)
	file, err = fileOrHashFile(afs, "./ok.txt")

	is.NoErr(err)
	is.Equal(file.Name(), "ok.txt")

	is.NoErr(afero.WriteFile(afs, "./ok.txt.xxh64", []byte{}, 0755))

	file, err = fileOrHashFile(afs, "./ok.txt")
	is.NoErr(err)
	is.Equal(file.Name(), "ok.txt.xxh64")

}

func TestComputeHash(t *testing.T) {
	is := is.New(t)

	buf := bytes.NewBuffer([]byte("Nobody inspects the spammish repetition"))
	hash := computeHash(buf)

	is.Equal(fmt.Sprintf("%x", hash), "fbcea83c8a378bf1")
}

func TestFsMove(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(fs.MkdirAll("old", 0755))

	ctrl, err := NewFsController(&Channel{}, fs)
	is.NoErr(err)

	dc := &model.DummyController{}
	is.Equal(ctrl.Move(&bytes.Buffer{}, dc), DecodeOrFail(&bytes.Buffer{}, dc, dc))

	ctrl.fs = afero.NewReadOnlyFs(fs)
	is.Equal(ctrl.Move(encodeJSON(is, MoveData{"old", "new"}), dc), ctrl.fs.Rename("old", "new"))

	_, ch := ctrl.ch.Subscribe()
	finish := make(chan sse.Event)
	go func() {
		val := <-ch
		finish <- val
	}()

	ctrl.fs = fs
	err = ctrl.Move(encodeJSON(is, MoveData{"old", "new"}), dc)
	is.NoErr(err)

	_, err = fs.Stat("new")
	is.NoErr(err)

	_, err = fs.Stat("old")
	is.True(err != nil)

	is.Equal(<-finish, EventFsMove("old", "new"))
}

type dontOpenFs struct {
	afero.Fs
	disabled string
}

func (f dontOpenFs) OpenFile(name string, flag int, perm fs.FileMode) (afero.File, error) {
	if name == f.disabled {
		return nil, io.ErrUnexpectedEOF
	}

	return f.Fs.OpenFile(name, flag, perm)
}

func TestFsVerify(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(fs, "src", []byte("hello world"), 0755))
	is.NoErr(afero.WriteFile(fs, "diff_size", []byte("hello"), 0755))
	is.NoErr(afero.WriteFile(fs, "same_size", []byte("hello worlb"), 0755))
	is.NoErr(afero.WriteFile(fs, "same", []byte("hello world"), 0755))
	is.NoErr(afero.WriteFile(fs, "diff_saved_as_checksum", []byte("asdfg okilp"), 0755))

	sum := xxhash.New()
	sum.Write([]byte("hello world"))
	hashie := strconv.FormatUint(sum.Sum64(), 16)
	is.NoErr(afero.WriteFile(fs, "same_saved_as_checksum", []byte("asdfg okilp"), 0755))
	is.NoErr(afero.WriteFile(fs, "same_saved_as_checksum.xxh64", []byte(hashie), 0755))

	ctrl, err := NewFsController(&Channel{}, fs)
	if err != nil {
		t.Fatalf("NewFsController: %s", err.Error())
	}

	dc := &model.DummyController{}
	is.Equal(ctrl.Verify(&bytes.Buffer{}, dc),
		DecodeOrFail(&bytes.Buffer{}, dc, dc))

	is.Equal(ctrl.Verify(encodeJSON(is, MoveData{Src: "404"}), dc),
		onlyLast(fileSize(ctrl.fs, "404")).(error))

	is.Equal(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "404"}), dc),
		onlyLast(fileSize(ctrl.fs, "404")).(error))

	is.True(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "diff_size"}), dc) != nil)

	oldFs := ctrl.fs
	ctrl.fs = dontOpenFs{oldFs, "src"}
	is.Equal(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "same_size"}), dc), io.ErrUnexpectedEOF)
	ctrl.fs = dontOpenFs{oldFs, "same_size"}
	is.Equal(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "same_size"}), dc), io.ErrUnexpectedEOF)

	ctrl.fs = oldFs
	is.True(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "same_size"}), dc) != nil)
	is.Equal(dc.GetError().ID, "fs-src-dst-hash-comparison")
	is.NoErr(ctrl.Verify(encodeJSON(is, MoveData{Src: "src", Dst: "same"}), dc))
}

func TestFsSize(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(fs, "src", []byte("hello world"), 0755))

	ctrl, err := NewFsController(&Channel{}, fs)
	is.NoErr(err)

	dc := &model.DummyController{}

	is.Equal(onlyLast(ctrl.Size(&bytes.Buffer{}, dc)).(error), DecodeOrFail(&bytes.Buffer{}, dc, dc))

	is.Equal(onlyLast(ctrl.Size(encodeJSON(is, FsGenericData{"404"}), dc)).(error), onlyLast(calculateSize(fs, "404")).(error))

	size, err := ctrl.Size(encodeJSON(is, FsGenericData{
		Name: "src",
	}), dc)
	is.NoErr(err)

	stat, err := fs.Stat("src")
	is.NoErr(err)
	is.Equal(size, stat.Size())

	err = fs.MkdirAll("data", 0755)
	is.NoErr(err)

	is.NoErr(afero.WriteFile(fs, "data/1.txt", []byte("hello world the prequel"), 0755))
	is.NoErr(afero.WriteFile(fs, "data/2.txt", []byte("hello world the sequel"), 0755))

	stat, err = fs.Stat("data")
	is.NoErr(err)

	var want int64 = stat.Size() + 23 + 22
	dc = &model.DummyController{}
	have, err := ctrl.Size(encodeJSON(is, FsGenericData{
		Name: "data",
	}), dc)
	is.NoErr(err)

	is.Equal(want, have)
	val := &SizeValue{}
	is.NoErr(dc.Read(val))
	is.Equal(val.Size, want)
}

func TestGetHashFromFs(t *testing.T) {
	is := is.New(t)

	afs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(afs, "file.txt", []byte("hello world"), 0755))

	_, err := getHashFromFs(afs, "not-found.txt")
	is.True(err != nil)

	sum, err := getHashFromFs(afs, "file.txt")
	is.NoErr(err)
	is.Equal(fmt.Sprintf("%x", sum), "45ab6734b21e6968")

	is.NoErr(afero.WriteFile(afs, "file.txt.xxh64", []byte("acab6734b21e6968z"), 0755))

	file, err := afs.Open("file.txt.xxh64")

	is.NoErr(err)

	_, err = getHashFromFs(afs, "file.txt")
	is.Equal(err, onlyLast(readAsHash(file)).(error))

	is.NoErr(file.Close())

	is.NoErr(afs.Remove("file.txt.xxh64"))
	is.NoErr(afero.WriteFile(afs, "file.txt.xxh64", []byte("acab6734b21e6968"), 0755))

	sum, err = getHashFromFs(afs, "file.txt")
	is.NoErr(err)
	is.Equal(fmt.Sprintf("%x", sum), "acab6734b21e6968")
}

type symlinkFile struct {
	fs.FileInfo
}

func (s symlinkFile) Mode() fs.FileMode { return s.FileInfo.Mode() | fs.ModeSymlink }

func TestResolveSymlink(t *testing.T) {
	is := is.New(t)

	afs := &SymlinkFs{afero.NewMemMapFs(), map[string]string{}}

	is.NoErr(afs.MkdirAll("dir1/dir2", 0755))
	fi, err := afs.OpenFile("dir1/link", os.O_CREATE, 0755|fs.ModeSymlink)
	is.NoErr(err)

	stat, err := fi.Stat()
	is.NoErr(err)

	is.NoErr(afs.SymlinkIfPossible("dir1/link", "dir1/dir2"))

	fm := resolveSymlink(afs, model.OsFileInfo{symlinkFile{stat}, "dir1/link", "dir1/link", nil})
	fi.Close()

	absPath, err := afs.ReadlinkIfPossible("dir1/link")
	is.NoErr(err)
	is.Equal(absPath, "dir1/dir2")
	is.Equal(fm.AbsolutePath, "dir1/dir2")
}

type failableWalk struct {
	afero.Fs
	name string
}

func (f *failableWalk) Stat(name string) (fs.FileInfo, error) {
	if name == f.name {
		return nil, fmt.Errorf("ok")
	}

	return f.Fs.Stat(name)
}

func (f *failableWalk) LstatIfPossible(name string) (fs.FileInfo, bool, error) {
	if name == f.name {
		return nil, false, model.ErrSkipFile
	}

	return f.Fs.(afero.Lstater).LstatIfPossible(name)
}

func TestCalculateSize(t *testing.T) {
	is := is.New(t)
	afs := afero.NewMemMapFs()

	getErr := func(path string) error {
		return onlyLast(calculateSize(afs, path)).(error)
	}
	getSize := func(path string) int64 {
		size, err := calculateSize(afs, path)
		is.NoErr(err)
		return size
	}

	is.Equal(getErr("404"), onlyLast(afs.Stat("404")).(error))
	is.NoErr(afero.WriteFile(afs, "ok.txt", []byte{1, 2}, 0755))
	is.Equal(getSize("ok.txt"), int64(2))

	is.NoErr(afs.MkdirAll("dir1/dir2/dir3", 0755))
	is.NoErr(afero.WriteFile(afs, "dir1/data.txt", []byte{1, 2}, 0755))
	is.NoErr(afero.WriteFile(afs, "dir1/dir2/data.txt", []byte{1, 2, 3}, 0755))
	is.NoErr(afero.WriteFile(afs, "dir1/dir2/dir3/data.txt", []byte{1, 2, 3, 4}, 0755))

	oldFs := afs
	afs = &failableWalk{oldFs, "dir1/dir2"}
	is.Equal(getErr("dir1"), model.ErrSkipFile)

	afs = oldFs

	is.Equal(getSize("dir1"), int64(42+2+42+3+42+4))
}

func TestFileSize(t *testing.T) {
	is := is.New(t)
	afs := afero.NewMemMapFs()

	is.Equal(onlyLast(fileSize(afs, "ok.txt")).(error), onlyLast(afs.Stat("ok.txt")).(error))

	afs.MkdirAll("dir", 0755)
	is.Equal(onlyLast(fileSize(afs, "dir")).(error), fs.ErrInvalid)

	is.NoErr(afero.WriteFile(afs, "ok.txt", []byte{1, 2, 3}, 0755))
	size, err := fileSize(afs, "ok.txt")
	is.NoErr(err)
	is.Equal(size, int64(3))
}
