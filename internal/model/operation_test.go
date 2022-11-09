package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amoghe/distillog"
	"github.com/fujiwara/shapeio"
	"github.com/matryer/is"
	"github.com/spf13/afero"
	"github.com/thanhpk/randstr"
)

func initFS(t *testing.T) afero.Fs {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	errOut := func(filename string, data []byte) {
		is.NoErr(afero.WriteFile(fs, filename, data, 0755))
	}
	errOut("content/level1.txt", []byte("sad"))
	errOut("content/level2/ok.txt", []byte("das"))
	errOut("content/level3/dir1/ok.txt", []byte("das"))
	errOut("content/level4/dir1/dir2/ok.txt", []byte("das"))

	return fs
}

func TestProgressWriter(t *testing.T) {
	err1 := fmt.Errorf("error")
	pw := progressWriter(func(p []byte) (int, error) {
		return -1, err1
	})

	_, err2 := pw.Write(nil)

	is := is.New(t)
	is.Equal(err1, err2)
}

func TestFileInfoMarshalJSON(t *testing.T) {
	is := is.New(t)
	fs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(fs, "ok.txt", []byte("asd"), 0755))

	stat, err := fs.Stat("ok.txt")
	is.NoErr(err)

	fi := FileInfo{nil, stat, "path", "abspath"}
	of := NewOsFileInfo(stat, "path", "abspath")

	bytes1, err := fi.MarshalJSON()
	is.NoErr(err)

	bytes2, err := of.MarshalJSON()
	is.NoErr(err)

	is.Equal(bytes1, bytes2)
}

func TestNewOperation(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	collection, err := FsToCollection(fs)
	is.NoErr(err)

	_, err = NewOperation(Collection{}, afero.NewMemMapFs())
	is.True(err != nil)

	_, err = NewOperation(Collection{{File: nil, Fs: fs, Path: "asd"}}, fs)
	is.True(err != nil)

	stat, err := fs.Stat("content/level1.txt")
	is.NoErr(err)

	_, err = NewOperation(Collection{{File: stat, Fs: nil, Path: "asd"}}, fs)
	is.True(err != nil)

	_, err = NewOperation(Collection{{File: stat, Fs: fs, Path: ""}}, fs)
	is.True(err != nil)

	_, err = NewOperation(collection, afero.NewMemMapFs())
	is.NoErr(err)
}

func TestOperationSources(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	collection, err := FsToCollection(fs)
	is.NoErr(err)

	op, err := NewOperation(collection, afero.NewMemMapFs())
	is.NoErr(err)

	is.Equal(op.Sources(), op.src.getSlice())
}

type progressSetter struct {
	sync.Mutex
	m map[int]int64
}

func (p *progressSetter) Set(index int, size int64) {
	p.Lock()
	if p.m == nil {
		p.m = map[int]int64{}
	}

	p.m[index] = size
	p.Unlock()
}

func TestOperationDo(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	dst := afero.NewMemMapFs()
	collection, err := FsToCollection(fs)
	is.NoErr(err)

	op, err := NewOperation(collection, dst)
	is.NoErr(err)

	buf := &bufferCloser{bytes.NewBuffer(nil), false}
	op.SetLogger(distillog.NewStreamLogger("", buf))

	ps := &progressSetter{}
	op.SetProgress(ps)
	op.setStatus(123)
	is.True(op.Start() != nil)

	{ // test whether aborting an Operation stops it
		op.setStatus(Aborted)
		finish := make(chan struct{})
		go func() {
			op.do()
			close(finish)
		}()
		_, ok := <-op.err
		is.True(!ok)
		<-finish
	}

	{
		buf.Reset()
		// test whether pausing an operation pauses it;
		// but also if skipping "continues" the operation
		op.err = make(chan OperationError)
		op.setStatus(Paused)
		//op.SetRateLimit(1)

		finish := make(chan struct{})
		go func() {
			op.do()
			close(finish)
		}()
		<-time.After(time.Millisecond * 10)
		op.setStatus(Started)
		op.SetIndex(1)

		err, ok := <-op.err
		//t.Log(buf.String())
		is.Equal(err.Error, ErrSkipFile)
		is.True(ok)

		go op.closeChannels()
		<-op.err
		op.setStatus(Aborted)

		<-finish
	}

	oldDst := op.dst
	{
		// test whether mkdirIfNeeded returns an error or not
		buf.Reset()
		is.NoErr(oldDst.RemoveAll("."))
		op.dst = afero.NewReadOnlyFs(op.dst)
		op.err = make(chan OperationError)
		op.SetIndex(0)
		op.setStatus(Started)

		finish := make(chan struct{})
		go func() {
			op.do()
			close(finish)
		}()

		opErr := <-op.err
		str := opErr.Error.Error()
		is.True(strings.Contains(str, "mkdirIfNeeded"))
		op.setStatus(Aborted)
		<-finish
	}
	{
		// test whether Aborted could make io.Copy return
		// cancelled copy
		buf.Reset()

		op.SetIndex(0)
		op.setStatus(Started)
		op.SetRateLimit(256)
		op.err = make(chan OperationError)
		op.dst = oldDst

		finish := make(chan struct{})
		go func() {
			op.do()
			close(finish)
		}()
		go func() {
			<-time.After(time.Millisecond * 10)
			op.setStatus(Aborted)
		}()

		opErr, ok := <-op.err
		is.Equal(errors.Unwrap(opErr.Error), ErrCancelled)
		is.True(ok)

		file := op.Sources()[op.Index()]

		_, err = file.Fs.Stat(file.Path)
		is.NoErr(err)

		_, err = op.dst.Stat(file.Path)
		is.True(err != nil)

		<-finish
	}

	{
		buf.Reset()
		// test normal operation without errors
		finish := make(chan struct{})
		op.err = make(chan OperationError)
		op.SetIndex(0)
		op.SetRateLimit(0)
		op.setStatus(Started)
		go func() {
			op.do()
			close(finish)
		}()

		for i := 0; i < len(op.Sources()); i++ {
			err := op.Error()
			is.NoErr(err.Error)

			ps.Lock()
			val := ps.m[i]
			ps.Unlock()

			is.Equal(val, op.Sources()[i].File.Size())
		}

		<-finish
	}
}

func TestOperationAdd(t *testing.T) {
	is := is.New(t)

	addfs := afero.NewMemMapFs()
	err := addfs.Mkdir("nue", 0755)
	is.NoErr(err)

	fi, err := addfs.OpenFile("nue/nue.txt", os.O_CREATE|os.O_WRONLY, 0755)
	is.NoErr(err)

	_, err = fi.WriteString("asd")
	is.NoErr(err)
	is.NoErr(fi.Close())

	fs := initFS(t)
	dst := afero.NewMemMapFs()
	collection, err := FsToCollection(fs)
	if err != nil {
		t.Fatalf("FsToCollection: %s", err.Error())
	}

	op, err := NewOperation(collection, dst)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	op.Start()

	added := false
	for i := 0; i < len(op.Sources()); i++ {
		is.NoErr(op.Error().Error)

		if !added {
			srcs := op.Sources()
			collection, err := FsToCollection(addfs)
			is.NoErr(err)

			srcs = append(srcs, collection...)

			op.SetSources(srcs)
			added = true
		}
	}

	_, err = dst.Stat("nue")
	is.NoErr(err)
}

func TestOperationPauseResume(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	dst := afero.NewMemMapFs()
	collection, err := FsToCollection(fs)
	is.NoErr(err)

	op, err := NewOperation(collection, dst)
	is.NoErr(err)

	op.Start()
	op.Pause()
	op.Resume()

	for i := 0; i < len(op.Sources()); i++ {
		is.NoErr(op.Error().Error)
	}

}

type bufferCloser struct {
	*bytes.Buffer
	close bool
}

func (b *bufferCloser) Close() error {
	b.close = true
	return nil
}

func TestOperationProceed(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	dst := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(dst, "content/level1.txt", []byte("asdad"), 0755))

	collection, err := FsToCollection(fs)
	is.NoErr(err)

	op, err := NewOperation(collection, dst)
	///buf := &bufferCloser{bytes.NewBuffer(nil), false}
	//op.logger.setLogger(distillog.NewStdoutLogger(""))

	is.NoErr(err)
	is.NoErr(op.Start())

	i := 0
	for {
		err := op.Error()
		if errors.Unwrap(err.Error) == ErrDstAlreadyExists {
			dst.Remove("content/level1.txt")
			op.Resume()

			continue
		}

		t.Log(op.Sources()[i])
		i++

		if i >= len(op.Sources()) {
			break
		}
	}
}

func TestOperationSkip(t *testing.T) {
	is := is.New(t)
	fs := initFS(t)

	dst := afero.NewMemMapFs()
	collection, err := FsToCollection(fs)
	is.NoErr(err)

	op, err := NewOperation(collection, dst)
	is.NoErr(err)

	op.SetRateLimit(512)
	op.Start()
	runs := 0
	for {
		err := op.Error()
		is.NoErr(err.Error)

		op.Pause()
		if op.src.index == 1 {
			op.src.index = 3
		}
		op.Resume()

		runs++

		if op.Index()+1 > len(op.Sources()) {
			break
		}
	}

	if runs != 2 && runs != 3 {
		t.Fatalf("%d runs must be either 2 or 3", runs)
	}
}

func TestMkdirIfNeeded(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(mkdirIfNeeded(fs, "/path/asd"))
	is.True(mkdirIfNeeded(afero.NewReadOnlyFs(fs), "/asd/asd/fasd") != nil)

	_, err := fs.Stat("/path")
	is.NoErr(err)

	is.NoErr(mkdirIfNeeded(fs, "/asdf"))
	_, err = fs.Stat("/asdf")
	is.True(err != nil)
}

func TestRemoveIfErrNotNil(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()
	is.NoErr(afero.WriteFile(fs, "/asd", []byte("asd"), 0755))

	_, err := fs.Stat("/asd")
	is.NoErr(err)

	srcFile := FileInfo{Path: "/asd", Fs: fs}
	removeIfErrNotNil(nil, srcFile)

	_, err = fs.Stat("/asd")
	is.NoErr(err)

	removeIfErrNotNil(ErrCancelled, srcFile)
	_, err = fs.Stat("/asd")
	is.True(err != nil)
}

type closer struct {
	close bool
}

func (c *closer) Close() error {
	c.close = true

	return nil
}

func TestCloseAll(t *testing.T) {
	is := is.New(t)

	v1, v2 := &closer{}, &closer{}

	closeAll(v1, v2)
	is.Equal(v1.close, true)
	is.Equal(v2.close, true)
}

func TestOpenSrcAndDir(t *testing.T) {
	is := is.New(t)
	srcfs := afero.NewMemMapFs()

	is.NoErr(afero.WriteFile(srcfs, "ok.txt", []byte("asd"), 0755))
	dstfs := afero.NewMemMapFs()

	_, _, err := openSrcAndDir(srcfs, srcfs, "ok.txt")
	is.Equal(err, ErrDstAlreadyExists)

	_, _, err = openSrcAndDir(srcfs, afero.NewReadOnlyFs(dstfs), "ok.txt")
	is.Equal(errors.Unwrap(err), ErrDstFile)

	_, _, err = openSrcAndDir(afero.NewMemMapFs(), dstfs, "ok.txt")
	is.Equal(errors.Unwrap(err), ErrSrcFile)

	is.NoErr(dstfs.RemoveAll("ok.txt"))

	_, _, err = openSrcAndDir(srcfs, dstfs, "ok.txt")
	is.NoErr(err)
}

func TestOperationReaderRead(t *testing.T) {
	buf := bytes.NewBufferString(randstr.String(2048))

	newIndex := 0
	status := uint8(Started)

	rateLimit := float64(16384 * 2)

	rd := &operationReader{
		reader:       shapeio.NewReader(buf),
		getIndex:     func() int { return newIndex },
		cachedIndex:  0,
		getStatus:    func() uint8 { return status },
		getRateLimit: func() float64 { return rateLimit },
	}
	rd.reader.SetRateLimit(rateLimit)

	// testing for SetRateLimit
	i := 0
	for {
		start := time.Now()
		slice := make([]byte, 1536)
		_, err := rd.Read(slice)
		if err == io.EOF {
			break
		}

		dur := time.Millisecond * 40
		if i == 0 && time.Since(start) < dur {
			t.Fatalf("rate limiter implementation does not work: %v < %v", time.Since(start), dur)
		}
		i++

		if i == 1 {
			rateLimit = 0
		}
	}

	buf.WriteString("ab")
	rateLimit = 0

	// test for skip file
	for i := 0; i < 2; i++ {
		slice := make([]byte, 1)
		_, err := rd.Read(slice)
		if i == 1 {
			if err == ErrSkipFile {
				break
			} else {
				t.Fatalf("Read should be skipped already: %v", err)
			}
		}

		newIndex = 2
	}

	newIndex = 0
	// test for ErrCancelled
	buf.WriteString("ba")
	for i := 0; i < 2; i++ {
		slice := make([]byte, 1)
		_, err := rd.Read(slice)
		if i == 1 {
			if err == ErrCancelled {
				break
			} else {
				t.Fatalf("Read should be aborted already: %v", err)
			}
		}

		status = Aborted
	}

	status = Started

	// test for ErrPause
	buf.WriteString("bac")
	for i := 0; i < 3; i++ {
		start := time.Now()

		slice := make([]byte, 1)
		rd.Read(slice)

		if i == 0 {
			status = Paused

			go func() {
				time.Sleep(opPauseDelay)
				status = Started
			}()
		} else if i == 1 {
			if time.Since(start) < opPauseDelay {
				t.Fatalf("pause doesn't work")
			}
		}
	}
}

func TestOperationWriterWrite(t *testing.T) {
	is := is.New(t)
	writer := &bytes.Buffer{}

	m := &progressSetter{}

	opWr := &operationWriter{m, 0, writer, 0}
	for i := 0; i < 2; i++ {
		if i == 0 {
			opWr.Write([]byte("asd"))
		} else if i == 1 {
			opWr.Write([]byte("bac"))
		}

		if i == 1 {
			is.Equal(m.m[0], int64(6))
			is.Equal(opWr.size, int64(6))
		}
	}
}

func TestPublicOperationMap(t *testing.T) {
	is := is.New(t)

	p := &PublicOperation{Operation: &Operation{}}
	p.logger = &logger{log: distillog.NewNullLogger("")}
	p.src = &sorcerer{index: 0}
	p.SetIndex(0)
	p.src.getSlice()
	p.setStatus(Aborted)
	p.Destination = "asdf"

	m := p.Map()

	is.Equal(m["index"], p.Index())
	is.Equal(m["src"], p.src.getSlice())
	is.Equal(m["status"], p.getStatus())
	is.Equal(m["dst"], p.Destination)
}

func TestPublicOperationMarshalJSON(t *testing.T) {
	is := is.New(t)

	p := &PublicOperation{Operation: &Operation{}}
	p.logger = &logger{log: distillog.NewNullLogger("")}
	p.src = &sorcerer{index: 0}
	p.SetIndex(0)
	p.src.getSlice()
	p.setStatus(Aborted)
	p.Destination = "asdf"

	m := p.Map()

	bytes1, err1 := p.MarshalJSON()
	bytes2, err2 := json.Marshal(m)

	is.NoErr(err1)
	is.NoErr(err2)

	is.Equal(bytes1, bytes2)
}

type failableWalk struct {
	afero.Fs
}

func (f *failableWalk) Stat(name string) (fs.FileInfo, error) {
	if name == "dir1" {
		return nil, fmt.Errorf("ok")
	}

	return f.Fs.Stat(name)
}

func (f *failableWalk) LstatIfPossible(name string) (fs.FileInfo, bool, error) {
	if name == "dir1" {
		return nil, false, ErrSkipFile
	}

	return f.Fs.(afero.Lstater).LstatIfPossible(name)
}

func TestFsToCollection(t *testing.T) {
	is := is.New(t)

	afs := &failableWalk{afero.NewMemMapFs()}
	is.NoErr(afs.Mkdir("dir1", 0755))
	_, err := FsToCollection(afs)

	is.Equal(err, ErrSkipFile)

	_, err = FsToCollection(afs.Fs)
	is.Equal(err, nil)
}

func TestDirToCollection(t *testing.T) {
	is := is.New(t)

	fs := afero.NewMemMapFs()

	err := fs.MkdirAll("new/new2/new3", 0755)
	is.NoErr(err)

	err = fs.MkdirAll("dir1/dir2", 0755)
	is.NoErr(err)

	writeFile := func(path string) {
		err = afero.WriteFile(fs, path, []byte{1, 2}, 0755)
		is.NoErr(err)
	}

	writeFile("new/new2/new3/ok.txt")
	writeFile("dir1/file1.txt")
	writeFile("dir1/dir2/file2.txt")
	writeFile("dir1/dir2/file3.txt")

	oldFs := fs
	fs = &failableWalk{afero.NewMemMapFs()}
	is.NoErr(fs.Mkdir("dir1", 0755))

	collect, err := DirToCollection(fs, "dir1")
	_, err2 := FsToCollection(fs)
	is.Equal(err, err2)

	fs = oldFs
	collect, err = DirToCollection(fs, "dir1")
	is.NoErr(err)

	for _, v := range collect {
		t.Log(fs.Stat(v.Path))
		//t.Log(v.Fs, v.AbsPath)
		_, err := v.Fs.Stat(v.Path)
		is.NoErr(err)
	}

	collect, err = DirToCollection(fs, "new/new2/new3")
	is.Equal(collect[0].AbsPath, "new/new2/new3/ok.txt")
	is.Equal(collect[0].Path, "new3/ok.txt")

	_, err = collect[0].Fs.Stat(collect[0].Path)
	is.NoErr(err)

	_, err = fs.Stat(collect[0].AbsPath)
	is.NoErr(err)
}

func TestOperationSetLogger(t *testing.T) {
	is := is.New(t)

	op := &Operation{logger: &logger{}}
	log := distillog.NewNullLogger("asd")
	op.logger.setLogger(log)

	is.Equal(op.logger.log, log)
}

func TestOperationStatus(t *testing.T) {
	op := &Operation{}

	is.New(t).Equal(op.Status(), op.status)
}

func TestOperationDestination(t *testing.T) {
	is := is.New(t)

	op := &Operation{}
	is.Equal(op.Destination(), op.dst)
}

func TestOperationSize(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)

	var size int64 = 0
	afero.Walk(afs, ".", func(path string, file os.FileInfo, err error) error {
		if !file.IsDir() {
			size += file.Size()
		}

		return nil
	})

	collect, err := FsToCollection(afs)
	is.NoErr(err)

	op, err := NewOperation(collect, afs)
	is.NoErr(err)

	is.Equal(op.Size(), size)
}

func TestOperationSetIndex(t *testing.T) {
	is := is.New(t)

	op := &Operation{}
	op.logger = &logger{log: distillog.NewNullLogger("")}
	op.src = &sorcerer{}

	is.Equal(op.src.index, 0)
	op.SetIndex(124)
	is.Equal(op.src.index, 124)
}

func TestOperationExit(t *testing.T) {
	is := is.New(t)

	op := &Operation{}
	op.logger = &logger{log: distillog.NewNullLogger("")}

	is.True(op.Exit() != nil)
	op.setStatus(Started)

	op.err = make(chan OperationError)
	is.NoErr(op.Exit())
	is.Equal(op.err, nil)
}
