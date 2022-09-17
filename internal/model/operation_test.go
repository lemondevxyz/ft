package model

import (
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func initFS() (afero.Fs, error) {
	fs := afero.NewMemMapFs()
	err := fs.Mkdir("content", 0755)
	if err != nil {
		return nil, err
	}

	fi, err := fs.OpenFile("content/level1.txt", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return nil, err
	}

	_, err = fi.WriteString("asdsad")
	if err != nil {
		return nil, err
	}
	fi.Close()

	err = fs.MkdirAll("content/level2/", 0755)
	if err != nil {
		return nil, err
	}

	fi, err = fs.OpenFile("content/level2/ok.txt", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return nil, err
	}

	fi.WriteString("asd")
	fi.Close()

	return fs, nil
}

func TestNewOperation(t *testing.T) {
	fs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	_, err = NewOperation(FsToCollection(fs), afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}
}

func TestOperationSources(t *testing.T) {
	fs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	op, err := NewOperation(FsToCollection(fs), afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	if !reflect.DeepEqual(op.Sources(), op.src) {
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
	fs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	dst := afero.NewMemMapFs()
	op, err := NewOperation(FsToCollection(fs), dst)
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

		if ps.m[i] != op.Sources()[i].File.Size() {
			t.Fatalf("file size mismatch: want: %d, have: %d", op.Sources()[i].File.Size(), ps.m[i])
		} else {
			t.Logf(op.Sources()[i].Path)
			t.Logf("have: %d", ps.m[i])
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

	fs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	dst := afero.NewMemMapFs()
	op, err := NewOperation(FsToCollection(fs), dst)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	op.Start()

	added := false
	for i := 0; i < len(op.Sources()); i++ {
		err := op.Error()
		if err.Error != nil {
			t.Log(err.Error)
		}

		if !added {
			srcs := op.Sources()
			srcs = append(srcs, FsToCollection(addfs)...)

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
	fs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	dst := afero.NewMemMapFs()
	op, err := NewOperation(FsToCollection(fs), dst)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	op.Start()
	op.Pause()
	start := time.Now()
	time.Sleep(time.Millisecond * 50)
	op.Resume()

	for i := 0; i < len(op.Sources()); i++ {
		err := op.Error()
		if err.Error != nil {
			t.Log(err.Error)
		}
	}

	if time.Now().Sub(start) <= time.Millisecond*50 {
		t.Fatalf("pause doesn't work properly")
	}
}
