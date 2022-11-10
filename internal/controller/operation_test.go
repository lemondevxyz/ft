package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"sync"
	"time"

	//	"fmt"
	"testing"

	"github.com/amoghe/distillog"
	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/matryer/is"
	"github.com/spf13/afero"
)

func initFS(t *testing.T) afero.Fs {
	is := is.New(t)
	afs := afero.NewMemMapFs()

	is.NoErr(afero.WriteFile(afs, "src/ok.txt", []byte("hello"), 0755))
	is.NoErr(afero.WriteFile(afs, "src/content/ok.txt", []byte("hello part 2"), 0755))
	is.NoErr(afero.WriteFile(afs, "src/content/ok2.txt", []byte("hello world"), 0755))
	is.NoErr(afero.WriteFile(afs, "src/content/dir/file.txt", []byte("hello world"), 0755))
	is.NoErr(afero.WriteFile(afs, "new.txt", []byte("new txt"), 0755))
	is.NoErr(afs.MkdirAll("dst", 0755))

	return afs
}

func encodeJSON(is *is.I, val interface{}) *bytes.Buffer {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	is.NoErr(enc.Encode(val))

	return buf
}

func TestOperationLogger(t *testing.T) {
	is := is.New(t)

	channel := &Channel{}
	_, ch := channel.Subscribe()

	ol := &operationLogger{channel, "ASD", sync.Mutex{}}

	equal := func(msg string) {
		have := <-ch
		want := EventOperationLog("ASD", msg)

		is.Equal(want, have)
	}

	go ol.Debugf("message")
	equal("[DEBUG] message")
	go ol.Debugln("message")
	equal("[DEBUG] message\n")

	go ol.Infof("message")
	equal("[INFO] message")
	go ol.Infoln("message")
	equal("[INFO] message\n")

	go ol.Warningf("message")
	equal("[WARNING] message")
	go ol.Warningln("message")
	equal("[WARNING] message\n")

	go ol.Errorf("message")
	equal("[ERROR] message")
	go ol.Errorln("message")
	equal("[ERROR] message\n")

	is.NoErr(ol.Close())
}

func TestSubscribe(t *testing.T) {
	is := is.New(t)

	ch := &Channel{}

	id, recv := ch.Subscribe()
	is.True(recv != nil)

	_, ok := ch.m[id]
	is.True(ok)
}

func TestGetSubscriber(t *testing.T) {
	is := is.New(t)
	ch := &Channel{}

	id, _ := ch.Subscribe()
	is.True(ch.GetSubscriber(id) != nil)
}

func TestUnsubscribe(t *testing.T) {
	is := is.New(t)
	ch := &Channel{}

	id, _ := ch.Subscribe()
	ch.Unsubscribe(id)
	_, ok := ch.m[id]
	is.True(!ok)
}

func TestAnnounce(t *testing.T) {
	is := is.New(t)
	channel := &Channel{}
	_, ch := channel.Subscribe()

	recv := make(chan sse.Event)
	go func() {
		val := <-ch
		recv <- val
	}()

	channel.Announce(sse.Event{Event: "test"})

	val := <-recv
	is.Equal(val.Event, "test")
}

func onlyLast(val ...interface{}) interface{} {
	return val[len(val)-1]
}

func TestNewOperationController(t *testing.T) {
	is := is.New(t)

	_, err1 := NewOperationController(nil, nil)
	_, err2 := NewOperationController(&Channel{}, nil)
	_, err3 := NewOperationController(nil, afero.NewMemMapFs())

	is.Equal(err1, err2)
	is.Equal(err2, err3)

	is.True(onlyLast(NewOperationController(&Channel{}, afero.NewMemMapFs())) == nil)
}

func TestOperationOperations(t *testing.T) {
	is := is.New(t)

	oc := &OperationController{}
	m := map[string]*Operation{}
	oc.operations = m

	is.Equal(oc.Operations(), oc.operations)
}

func TestOperationControllerAddOperation(t *testing.T) {
	is := is.New(t)
	channel := &Channel{}

	ev := make(chan sse.Event)
	owner, ch := channel.Subscribe()
	go func() {
		val := <-ch
		ev <- val
	}()

	oc, err := NewOperationController(channel, afero.NewMemMapFs())
	is.NoErr(err)

	is.True(onlyLast(oc.AddOperation(nil, "dst", "aerc")) != nil)
	id, err := oc.AddOperation(nil, "", owner)
	is.NoErr(err)
	_, ok := oc.operations[id]
	t.Log(oc.operations)
	is.True(ok)
}

func TestOperationControllerGetOperation(t *testing.T) {
	is := is.New(t)

	channel := &Channel{}

	oc, err := NewOperationController(channel, afero.NewMemMapFs())
	is.NoErr(err)

	oc.operations["asd"] = nil
	is.True(onlyLast(oc.GetOperation("")).(error) != nil)
}

func TestOperationControllerNewOperation(t *testing.T) {
	is := is.New(t)

	afs := initFS(t)
	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}
	err = onlyLast(oc.NewOperation(&bytes.Buffer{}, ctrl)).(error)
	is.Equal(errors.Unwrap(err), io.EOF)

	data := OperationNewData{
		WriterID: "",
		Src:      []string{"404"},
		Dst:      "404",
	}

	createOp := func() error {
		return onlyLast(oc.NewOperation(encodeJSON(is, data), ctrl)).(error)
	}

	is.Equal(createOp(),
		onlyLast(oc.ConvertPathsToFilesOrFail(ctrl, data.Src)).(error))

	data.Src = []string{"src"}

	is.Equal(createOp(),
		onlyLast(StatOrFail(ctrl, afs, data.Dst)).(error))
	data.Dst = "dst"

	is.Equal(createOp(),
		onlyLast(oc.AddOperation(nil, "", "")).(error))

	id, ch := channel.Subscribe()
	finish := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			<-ch
		}
		close(finish)
	}()

	data.WriterID = id
	is.True(onlyLast(oc.NewOperation(encodeJSON(is, data), ctrl)) == nil)

	<-finish
}

func TestOperationControllerStatus(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)

	channel := &Channel{}

	id, ch := channel.Subscribe()
	finish := make(chan struct{})
	go func() {
		for i := 0; i < 4; i++ {
			<-ch
		}
		close(finish)
	}()

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}

	is.Equal(errors.Unwrap(oc.Status(bytes.NewBuffer(nil), ctrl)), io.EOF)
	is.True(oc.Status(encodeJSON(is, &OperationStatusData{
		ID:     "",
		Status: 0,
	}), ctrl) != nil)

	opVal, err := oc.NewOperation(encodeJSON(is, &OperationNewData{
		WriterID: id,
		Src:      []string{"src"},
		Dst:      "dst",
	}), ctrl)
	is.NoErr(err)

	op := oc.operations[opVal.ID]
	op.SetLogger(distillog.NewNullLogger(""))
	op.SetRateLimit(1)

	for k, status := range []uint8{model.Finished, model.Started, model.Paused, model.Aborted} {
		var fn func() error
		switch status {
		case model.Paused:
			is.NoErr(op.Pause())
			fn = op.Resume
		case model.Started:
			is.NoErr(op.Start())
			fn = op.Pause
		case model.Aborted:
			is.NoErr(op.Exit())
		}

		err = oc.Status(encodeJSON(is, &OperationStatusData{
			ID:     opVal.ID,
			Status: status,
		}), ctrl)
		if k == 0 {
			is.True(err != nil)
		} else {
			if fn != nil {
				is.True(err != nil)

				fn()

				err = oc.Status(encodeJSON(is, &OperationStatusData{
					ID:     opVal.ID,
					Status: status,
				}), ctrl)
			}

			if status != model.Aborted {
				is.NoErr(err)
				is.Equal(status, op.Status())
			}
		}
	}

	<-finish
}

func TestOperationControllerSetSources(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)
	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	ctrl := &model.DummyController{}
	is.Equal(oc.SetSources(&bytes.Buffer{}, ctrl), DecodeOrFail(&bytes.Buffer{}, ctrl, nil))
	is.True(oc.SetSources(encodeJSON(is, &OperationSetSourcesData{
		ID:   "",
		Srcs: []string{},
	}), ctrl) != nil)

	is.Equal(oc.SetSources(encodeJSON(is, &OperationSetSourcesData{
		ID:   "",
		Srcs: []string{"404"},
	}), ctrl), onlyLast(oc.ConvertPathsToFilesOrFail(ctrl, []string{"404"})).(error))

	is.Equal(oc.SetSources(encodeJSON(is, &OperationSetSourcesData{
		ID:   "",
		Srcs: []string{"src"},
	}), ctrl), onlyLast(oc.GetOperation("")).(error))

	id, ch := channel.Subscribe()
	finish := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			val := <-ch
			t.Log(val)
		}
		close(finish)
	}()

	val, err := oc.NewOperation(encodeJSON(is, OperationNewData{
		WriterID: id,
		Src:      []string{"src"},
		Dst:      "dst",
	}), ctrl)
	is.NoErr(err)

	oc.operations[val.ID].SetLogger(distillog.NewNullLogger(""))
	is.NoErr(oc.SetSources(encodeJSON(is, &OperationSetSourcesData{
		ID:   val.ID,
		Srcs: []string{"src/ok.txt"},
	}), ctrl))

	is.Equal(len(oc.operations[val.ID].Sources()), 1)
	<-finish
}

func TestOperationControllerSetIndex(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)
	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}
	is.Equal(oc.SetIndex(bytes.NewBuffer(nil), ctrl), DecodeOrFail(bytes.NewBuffer(nil), ctrl, nil))

	data := &OperationSetIndexData{"", 0}
	is.Equal(oc.SetIndex(encodeJSON(is, data), ctrl),
		onlyLast(oc.GetOperation("")).(error))

	id, ch := channel.Subscribe()
	closed := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			ev := <-ch
			t.Log(ev)
		}
		close(closed)
	}()

	val, err := oc.NewOperation(encodeJSON(is, OperationNewData{
		WriterID: id,
		Src:      []string{"src"},
		Dst:      "dst",
	}), ctrl)
	is.NoErr(err)

	data.Index = 125
	data.ID = val.ID

	is.True(oc.SetIndex(encodeJSON(is, data), ctrl) != nil)

	data.Index = 2

	is.NoErr(oc.SetIndex(encodeJSON(is, data), ctrl))
	is.Equal(oc.operations[val.ID].Index(), 2)

	<-closed
}

func TestOperationProceed(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)

	channel := &Channel{}

	t.Log("NewOperationController")
	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}
	is.Equal(oc.Proceed(&bytes.Buffer{}, ctrl), DecodeOrFail(&bytes.Buffer{}, ctrl, ctrl))

	body := encodeJSON(is, &OperationGenericData{
		ID: "404",
	})
	is.Equal(oc.Proceed(body, ctrl), onlyLast(oc.GetOperationOrFail(ctrl, "404")).(error))

	files, err := model.DirToCollection(afs, "src")
	is.NoErr(err)

	t.Log("NewOperation")
	op, err := model.NewOperation(files, afero.NewBasePathFs(afs, "dst"))
	t.Log("AddOperation")
	_ = channel.SetSubscriber("owner")
	id, err := oc.AddOperation(op, "dst", "owner")
	is.NoErr(err)

	t.Log("op.Start()")
	is.NoErr(op.Start())
	is.NoErr(afero.WriteFile(afs, "dst/src/content/dir/file.txt", []byte{1, 2}, 0755))

	finish := make(chan struct{})
	go func() {
		op.Error()
		close(finish)
	}()

	t.Log("Proceed")
	is.NoErr(oc.Proceed(encodeJSON(is, &OperationGenericData{id}), ctrl))

	t.Log("Status")
	is.Equal(op.Status(), model.Started)

	t.Log("Finish")
	<-finish
	t.Log("done")
}

func TestOperationSetRateLimit(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)

	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}
	is.Equal(oc.SetRateLimit(&bytes.Buffer{}, ctrl), DecodeOrFail(&bytes.Buffer{}, ctrl, ctrl))

	body := encodeJSON(is, &OperationGenericData{
		ID: "404",
	})
	is.Equal(oc.SetRateLimit(body, ctrl), onlyLast(oc.GetOperationOrFail(ctrl, "404")).(error))

	files, err := model.DirToCollection(afs, "src")
	is.NoErr(err)

	op, err := model.NewOperation(files, afero.NewBasePathFs(afs, "dst"))
	is.NoErr(err)

	_ = channel.SetSubscriber("owner")
	id, err := oc.AddOperation(op, "dst", "owner")

	is.NoErr(err)
	is.NoErr(oc.SetRateLimit(encodeJSON(is, &OperationRateLimitData{id, 2.5}), ctrl))

	is.Equal(op.RateLimit(), 2.5)
}

type fileInfo struct {
	size int64
}

func (f fileInfo) Name() string       { return "name" }
func (f fileInfo) Size() int64        { return 1243 }
func (f fileInfo) Mode() fs.FileMode  { return 0 }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return false }
func (f fileInfo) Sys() interface{}   { return nil }

func TestOperationMarshalJSON(t *testing.T) {
	is := is.New(t)
	afs := initFS(t)

	collect, err := model.DirToCollection(afs, "src")
	is.NoErr(err)

	op, err := model.NewOperation(collect, afs)
	is.NoErr(err)

	po := &Operation{&model.PublicOperation{
		op,
		"asd",
	}, "asd", sync.Mutex{}}

	bytes, err := po.MarshalJSON()
	is.NoErr(err)

	m := map[string]interface{}{}

	is.NoErr(json.Unmarshal(bytes, &m))

	is.Equal(m["id"], "asd")
	is.Equal(m["size"], float64(39))
}

func TestProgressBroadcasterSet(t *testing.T) {
	pb := &ProgressBroadcaster{}
	pb.id = "abc"

	chnl := &Channel{}
	_, ch := chnl.Subscribe()

	pb.channel = chnl

	finish := make(chan struct{})
	go func() {
		for i := 0; i < 2; i++ {
			<-ch
		}

		close(finish)
	}()

	pb.Set(0, 124)
	pb.Set(0, 204)

	select {
	case <-finish:
		t.Fatalf("progress broadcaster doesn't wait for a second")
	case <-time.After(time.Millisecond):
	}
}

func TestOperationGoRoutine(t *testing.T) {
	is := is.New(t)

	channel := &Channel{}
	_, ch := channel.Subscribe()

	afs := initFS(t)

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	errCh, doneCh := make(chan sse.Event), make(chan sse.Event)
	go func() {
		for {
			val := <-ch
			if val.Event == "operation-done" {
				doneCh <- val
				close(doneCh)
				break
			} else if val.Event == "operation-error" {
				errCh <- val
				close(errCh)
			}
		}
	}()

	dc := &model.DummyController{}

	srcs, err := oc.ConvertPathsToFilesOrFail(dc, []string{"src"})
	is.NoErr(err)

	op, err := model.NewOperation(srcs, afero.NewBasePathFs(afs, "dst"))
	is.NoErr(err)

	oc.operations[""] = &Operation{
		PublicOperation: &model.PublicOperation{
			Operation:   op,
			Destination: "dst",
		}, ID: ""}

	op.SetRateLimit(1)

	is.NoErr(afero.WriteFile(afs, "dst/src/content/dir/file.txt", []byte{1, 2}, 0755))
	go oc.opGoRoutine(oc.operations[""])
	t.Log("starte")
	is.NoErr(op.Start())

	t.Log("err")
	<-errCh
	t.Log("exit")
	is.NoErr(op.Exit())

	t.Log("wait 4 doneCh")
	<-doneCh
	t.Log("finnish")
}

func TestConvertPathsToFilesOrFail(t *testing.T) {
	is := is.New(t)

	channel := &Channel{}
	//_, ch := channel.Subscribe()

	afs := initFS(t)

	oc, err := NewOperationController(channel, afs)
	is.NoErr(err)

	ctrl := &model.DummyController{}

	_, err = oc.ConvertPathsToFilesOrFail(ctrl, []string{"404"})
	is.Equal(err, onlyLast(StatOrFail(ctrl, afs, "404")).(error))

	oldFs := afs
	afs = &failableWalk{afs, "src/content"}

	oc.fs = afs
	_, err = oc.ConvertPathsToFilesOrFail(ctrl, []string{"src"})
	is.Equal(err, onlyLast(model.DirToCollection(afs, "src")).(error))

	oc.fs = oldFs
	collect, err := oc.ConvertPathsToFilesOrFail(ctrl, []string{"src/content", "src/ok.txt"})
	is.NoErr(err)

	is.Equal(len(collect), 4)
}
