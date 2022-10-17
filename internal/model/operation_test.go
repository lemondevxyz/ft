package model

import (
	"bytes"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func init() {
	opDelay = 0
}

func initFS(t *testing.T) afero.Fs {
	fs := afero.NewMemMapFs()
	errOut := func(filename string, data []byte) {
		err := afero.WriteFile(fs, filename, data, 0755)
		if err != nil {
			t.Fatalf("WriteFile: %s", err.Error())
		}
	}
	errOut("content/level1.txt", []byte("sad"))
	errOut("content/level2/ok.txt", []byte("das"))
	errOut("content/level3/dir1/ok.txt", []byte("das"))
	errOut("content/level4/dir1/dir2/ok.txt", []byte("das"))

	return fs
}

func TestDirToCollection(t *testing.T) {
	fs := afero.NewMemMapFs()

	err := fs.MkdirAll("new/new2/new3", 0755)
	if err != nil {
		t.Fatalf("fs.MkdirAll: %s", err.Error())
	}

	err = fs.MkdirAll("dir1/dir2", 0755)
	if err != nil {
		t.Fatalf("fs.MkdirAll: %s", err.Error())
	}

	writeFile := func(path string) {
		err = afero.WriteFile(fs, path, []byte{1, 2}, 0755)
		if err != nil {
			t.Fatalf("fs.WriteFile: %s", err.Error())
		}
	}

	writeFile("new/new2/new3/ok.txt")
	writeFile("dir1/file1.txt")
	writeFile("dir1/dir2/file2.txt")
	writeFile("dir1/dir2/file3.txt")

	collect, err := DirToCollection(fs, "dir1")
	if err != nil {
		t.Fatalf("DirToCollection: %s", err.Error())
	}

	for _, v := range collect {
		t.Log(v.Fs)
		_, err := v.Fs.Stat(v.Path)
		if err != nil {
			t.Fatalf("v.Fs.Stat: %s", err.Error())
		}
	}

	collect, err = DirToCollection(fs, "new/new2/new3")
	if collect[0].Path != "ok.txt" {
		t.Fatalf(`DirToCollection doesn't output the correct path`)
	}
	t.Log(collect[0].Path, err)
}

func TestNewOperation(t *testing.T) {
	fs := initFS(t)

	collection, err := FsToCollection(fs)
	if err != nil {
		t.Fatalf("FsToCollection: %s", err.Error())
	}

	_, err = NewOperation(collection, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}
}

func TestOperationSources(t *testing.T) {
	fs := initFS(t)

	collection, err := FsToCollection(fs)
	if err != nil {
		t.Fatalf("FsToCollection: %s", err.Error())
	}

	op, err := NewOperation(collection, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	if !reflect.DeepEqual(op.Sources(), op.src.getSlice()) {
		t.Fatalf("sources aren't equal")
	}
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

	ps := &progressSetter{}
	op.SetProgress(ps)
	op.Start()

	for i := 0; i < len(op.Sources()); i++ {
		err := op.Error()
		if err.Error != nil {
			t.Fatal(err.Error)
		}

		ps.Lock()
		val := ps.m[i]
		ps.Unlock()
		if val != op.Sources()[i].File.Size() {
			t.Fatalf("file size mismatch: want: %d, have: %d", op.Sources()[i].File.Size(), val)
		} else {
			t.Logf(op.Sources()[i].Path)
			t.Logf("have: %d", val)
			t.Logf("written: %d", op.Sources()[i].File.Size())
			t.Log()
		}
	}
}

func TestOperationAdd(t *testing.T) {
	addfs := afero.NewMemMapFs()
	err := addfs.Mkdir("nue", 0755)
	if err != nil {
		t.Fatalf("Mkdir: %s", err.Error())
	}

	fi, err := addfs.OpenFile("nue/nue.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatalf("OpenFile: %s", err.Error())
	}
	_, err = fi.WriteString("asd")
	if err != nil {
		t.Fatalf("WriteString: %s", err.Error())
	}
	fi.Close()

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
	t.Log("hya")
	for i := 0; i < len(op.Sources()); i++ {
		t.Log("henlo", op.Sources()[i])
		err := op.Error()
		if err.Error != nil {
			t.Log(err.Error)
		}

		if !added {
			srcs := op.Sources()
			collection, err := FsToCollection(addfs)
			if err != nil {
				t.Fatalf("FsToCollection: %s", err.Error())
			}

			srcs = append(srcs, collection...)

			op.SetSources(srcs)
			added = true
		}
	}

	_, err = dst.Stat("nue")
	if err != nil {
		t.Fatalf("dst.Stat: %s", err.Error())
	}
}

func TestOperationPauseResume(t *testing.T) {
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
	op.Pause()
	dur := opDelay * 2
	start := time.Now()
	time.Sleep(dur)
	op.Resume()

	for i := 0; i < len(op.Sources()); i++ {
		err := op.Error()
		if err.Error != nil {
			t.Log(err.Error)
		}
	}

	if time.Now().Sub(start) <= opDelay*2 {
		t.Fatalf("pause doesn't work properly")
	}
}

type bufferCloser struct {
	*bytes.Buffer
}

func (b *bufferCloser) Close() error { return nil }

func TestOperationProceed(t *testing.T) {
	fs := initFS(t)

	dst := afero.NewMemMapFs()
	err := afero.WriteFile(dst, "content/level1.txt", []byte("asdad"), 0755)
	if err != nil {
		t.Fatalf("afero.WriteFile: %s", err.Error())
	}

	collection, err := FsToCollection(fs)
	if err != nil {
		t.Fatalf("FsToCollection: %s", err.Error())
	}

	op, err := NewOperation(collection, dst)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}
	//op.SetLogger(distillog.NewStdoutLogger(""))
	op.Start()

	i := 0
	for {
		t.Log("oke")
		err := op.Error()
		if err.Error == ErrDstAlreadyExists {
			dst.Remove("content/level1.txt")
			op.Proceed()
		}

		t.Log(op.Sources()[i])
		i++

		if i >= len(op.Sources()) {
			break
		}
	}
}

func TestOperationSkip(t *testing.T) {
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
	runs := 0
	opDelay = time.Millisecond
	for {

		err := op.Error()
		if err.Error != nil {
			t.Log(err.Error)
		}
		op.mtx.Lock()
		if op.Index() == 1 {
			op.SetIndex(3)
		}
		t.Log(op.Index())
		runs++
		op.mtx.Unlock()

		if op.Index()+1 > len(op.Sources()) {
			break
		}
	}

	if runs > 2 {
		t.Fatalf("runs are more than %d", runs)
	}
}
