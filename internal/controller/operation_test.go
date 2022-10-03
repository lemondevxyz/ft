package controller

import (
	"bytes"
	"encoding/json"

	//	"fmt"
	"io/fs"
	"testing"

	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
)

func initFS(t *testing.T) afero.Fs {
	afs := afero.NewMemMapFs()

	err := afero.WriteFile(afs, "src/ok.txt", []byte("hello"), 0755)
	if err != nil {
		t.Fatalf("afs.WriteFile: %s", err.Error())
	}

	err = afero.WriteFile(afs, "src/content/ok.txt", []byte("hello part 2"), 0755)
	if err != nil {
		t.Fatalf("afs.WriteFile: %s", err.Error())
	}

	err = afero.WriteFile(afs, "src/content/ok2.txt", []byte("hello world"), 0755)
	if err != nil {
		t.Fatalf("afs.WriteFile: %s", err.Error())
	}

	err = afero.WriteFile(afs, "src/content/dir/file.txt", []byte("hello world"), 0755)
	if err != nil {
		t.Fatalf("afs.WriteFile: %s", err.Error())
	}

	err = afero.WriteFile(afs, "new.txt", []byte("new txt"), 0755)
	if err != nil {
		t.Fatalf("afero.WriteFile: %s", err.Error())
	}

	err = afs.MkdirAll("dst", 0755)
	if err != nil {
		t.Fatalf("afero.MkdirAll: %s", err.Error())
	}

	return afs
}

func encodeJSON(val interface{}) *bytes.Buffer {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.Encode(val)

	return buf
}

func TestSubscribe(t *testing.T) {
	ch := &Channel{}

	id, recv := ch.Subscribe()
	if recv == nil {
		t.Fatal("Subscribe has nil channel")
	}

	if _, ok := ch.m[id]; !ok {
		t.Fatalf("%s doesn't exist in subscribers", id)
	}
}

func TestGetSubscriber(t *testing.T) {
	ch := &Channel{}

	id, _ := ch.Subscribe()
	if ch.GetSubscriber(id) == nil {
		t.Fatalf("ch.GetSubscriber returns nil")
	}
}

func TestUnsubscribe(t *testing.T) {
	ch := &Channel{}

	id, _ := ch.Subscribe()
	ch.Unsubscribe(id)
	if _, ok := ch.m[id]; ok {
		t.Fatalf("%s does exist after Unsubscribe", id)
	}
}

func TestAnnounce(t *testing.T) {
	channel := &Channel{}
	_, ch := channel.Subscribe()

	recv := make(chan sse.Event)
	go func() {
		val := <-ch
		recv <- val
	}()

	channel.Announce(sse.Event{Event: "test"})

	val := <-recv
	if val.Event != "test" {
		t.Fatalf("invalid event: %v", val)
	}
}

func TestOperationControllerAddOperation(t *testing.T) {
	channel := &Channel{}

	ev := make(chan sse.Event)
	owner, ch := channel.Subscribe()
	go func() {
		val := <-ch
		ev <- val
	}()

	oc, err := NewOperationController(channel, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	id, err := oc.AddOperation(nil, "", owner)
	if err != nil {
		t.Fatalf("oc.AddOperation: %s", err.Error())
	}

	if _, ok := oc.operations[id]; !ok {
		t.Fatalf("oc.operations[%s] doesn't exist", id)
	}
}

func TestOperationControllerGetOperation(t *testing.T) {
	channel := &Channel{}

	ev := make(chan sse.Event)
	owner, ch := channel.Subscribe()
	go func() {
		val := <-ch
		ev <- val
	}()

	oc, err := NewOperationController(channel, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	id, err := oc.AddOperation(nil, "", owner)
	if err != nil {
		t.Fatalf("oc.AddOperation: %s", err.Error())
	}

	op, err := oc.GetOperation(id)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	if op == nil {
		t.Fatalf("operation %s is nil", id)
	}
}

func TestOperationControllerNewOperation(t *testing.T) {
	afs := initFS(t)

	channel := &Channel{}

	afero.Walk(afs, "src", func(path string, info fs.FileInfo, err error) error {
		t.Log(path)
		return nil
	})

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	id, ch := channel.Subscribe()

	ev := make(chan struct{})
	t.Log("before goroutine")
	go func() {
		for i := 0; i < 3; i++ {
			<-ch
		}
		ev <- struct{}{}
	}()

	ctrl := &model.DummyController{}
	newOpRes, err := oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src"},
		Dst:      "dst",
	}), ctrl)
	if err != nil {
		t.Fatalf("couldn't start operation: %s", err.Error())
	}

	op, err := oc.GetOperationOrFail(ctrl, newOpRes.ID)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	srcs := op.Sources()
	for _, v := range srcs {
		_, err := v.Fs.Stat(v.Path)
		if err != nil {
			t.Fatalf("afero.Stat: %s", err.Error())
		}
	}

	t.Log("waiting 4 event")
	<-ev
	t.Log("done; unsubscribe")
	channel.Unsubscribe(id)
	t.Log("done")
}

func TestOperationControllerStatus(t *testing.T) {
	afs := initFS(t)

	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	closed := make(chan struct{})

	id, ch := channel.Subscribe()

	open := true
	started := false

	go func() {
		for open {
			started = true
			<-ch
		}

		close(closed)
	}()

	for !started {
	}

	dummy := &model.DummyController{}
	res, err := oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src", "new.txt"},
		Dst:      "dst",
	}), dummy)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	if err := oc.Status(encodeJSON(OperationStatusData{res.ID, model.Started}), dummy); err != nil {
		t.Fatalf("oc.Start: %s", err.Error())
	}

	op, err := oc.GetOperationOrFail(dummy, res.ID)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	t.Log(op.Sources())

	if op.Status() != model.Started {
		t.Fatalf("Start(rd, oc) doesn't actually start the operation")
	}

	err = oc.Status(encodeJSON((OperationStatusData{res.ID, model.Paused})), dummy)
	if err != nil {
		t.Fatalf("oc.Pause: %s", err.Error())
	}

	if op.Status() != model.Paused {
		t.Fatalf("oc.Status isn't model.Paused: %d", op.Status())
	}

	err = oc.Status(encodeJSON(OperationStatusData{res.ID, model.Started}), dummy)
	if err != nil {
		t.Fatalf("oc.Pause: %s", err.Error())
	}

	if op.Status() != model.Started {
		t.Fatalf("oc.Status isn't model.Started: %d", op.Status())
	}

	err = oc.Status(encodeJSON(OperationStatusData{res.ID, model.Aborted}), dummy)
	if err != nil {
		t.Fatalf("oc.Exit: %s", err.Error())
	}

	if op.Status() != model.Aborted {
		t.Fatalf("oc.Status isn't model.Aborted")
	}

	t.Log("unsubscribe")
	channel.Unsubscribe(id)
	t.Log("open")
	open = false

	t.Log("closing")
	close(ch)
	t.Log("done")
	<-closed
	t.Log("fetching closed")

}

// DEPRECATED
func TestOperationControllerGeneric(t *testing.T) {
	afs := initFS(t)

	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	closed := make(chan struct{})

	id, ch := channel.Subscribe()

	open := true
	started := false

	go func() {
		for open {
			started = true
			<-ch
		}

		close(closed)
	}()

	for !started {
	}

	dummy := &model.DummyController{}
	res, err := oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src", "new.txt"},
		Dst:      "dst",
	}), dummy)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	if err := oc.Start(encodeJSON(OperationGenericData{res.ID}), dummy); err != nil {
		t.Fatalf("oc.Start: %s", err.Error())
	}

	op, err := oc.GetOperationOrFail(dummy, res.ID)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	t.Log(op.Sources())

	if op.Status() != model.Started {
		t.Fatalf("Start(rd, oc) doesn't actually start the operation")
	}

	err = oc.Pause(encodeJSON(OperationGenericData{res.ID}), dummy)
	if err != nil {
		t.Fatalf("oc.Pause: %s", err.Error())
	}

	if op.Status() != model.Paused {
		t.Fatalf("oc.Status isn't model.Paused: %d", op.Status())
	}

	err = oc.Resume(encodeJSON(OperationGenericData{res.ID}), dummy)
	if err != nil {
		t.Fatalf("oc.Pause: %s", err.Error())
	}

	if op.Status() != model.Started {
		t.Fatalf("oc.Status isn't model.Started: %d", op.Status())
	}

	err = oc.Exit(encodeJSON(OperationGenericData{res.ID}), dummy)
	if err != nil {
		t.Fatalf("oc.Exit: %s", err.Error())
	}

	if op.Status() != model.Aborted {
		t.Fatalf("oc.Status isn't model.Aborted")
	}

	t.Log("unsubscribe")
	channel.Unsubscribe(id)
	t.Log("open")
	open = false

	t.Log("closing")
	close(ch)
	t.Log("done")
	<-closed
	t.Log("fetching closed")

}

func TestOperationControllerSetSources(t *testing.T) {
	afs := initFS(t)
	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	closed := make(chan struct{})

	id, ch := channel.Subscribe()

	open := true
	started := false

	go func(t *testing.T) {
		for open {
			started = true

			ev := <-ch
			_ = ev

			open = false
		}

		close(closed)
	}(t)

	for !started {
	}

	dummy := &model.DummyController{}
	res, err := oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src", "new.txt"},
		Dst:      "dst",
	}), dummy)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	op, err := oc.GetOperationOrFail(dummy, res.ID)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	/*
		err = oc.Start(encodeJSON(OperationGenericData{
			ID: res.ID,
		}), dummy)
		if err != nil {
			t.Fatalf("oc.Start: %v", err.Error())
		}
	*/

	err = oc.SetSources(encodeJSON(OperationSetSourcesData{
		ID:   res.ID,
		Srcs: []string{},
	}), dummy)
	if err == nil {
		t.Fatalf("oc.SetSources shouldn't return nil, instead it should return an error saying that srcs cannot be empty")
	}

	err = oc.SetSources(encodeJSON(OperationSetSourcesData{
		ID:   res.ID,
		Srcs: []string{"empty lamo adsfasdf"},
	}), dummy)
	if err == nil {
		t.Fatalf("oc.SetSources shouldn't return nil, instead it should return an error saying that one of the files doesn't exist")
	}

	err = oc.SetSources(encodeJSON(OperationSetSourcesData{
		ID:   res.ID,
		Srcs: []string{"new.txt"},
	}), dummy)
	if err != nil {
		t.Fatalf("oc.SetSources: %s", err.Error())
	}

	srcs := op.Sources()
	if srcs[0].Path != "new.txt" {
		t.Fatalf("sources do not match wanted result")
	}

	<-closed
}

func TestOperationControllerSetIndex(t *testing.T) {
	afs := initFS(t)
	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	closed := make(chan struct{})

	id, ch := channel.Subscribe()

	//open := true
	started := false

	go func(t *testing.T) {
		for i := 0; i < 5; i++ {
			started = true

			ev := <-ch
			_ = ev
		}

		close(closed)
	}(t)

	for !started {
	}

	dummy := &model.DummyController{}
	res, err := oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src", "new.txt"},
		Dst:      "dst",
	}), dummy)
	if err != nil {
		t.Fatalf("NewOperation: %s", err.Error())
	}

	op, err := oc.GetOperationOrFail(dummy, res.ID)
	if err != nil {
		t.Fatalf("GetOperationOrFail: %s", err.Error())
	}

	/*
		err = oc.Start(encodeJSON(OperationGenericData{
			ID: res.ID,
		}), dummy)
		if err != nil {
			t.Fatalf("oc.Start: %v", err.Error())
		}
	*/

	t.Log("asdf", op)
	err = oc.SetIndex(encodeJSON(OperationSetIndexValue{
		ID:    res.ID,
		Index: 12,
	}), dummy)
	if err == nil {
		t.Fatalf("oc.SetIndex shouldn't return nil, instead it should return an error saying that the index is bigger than expected")
	}

	t.Log("Asdf", op)
	err = oc.SetIndex(encodeJSON(OperationSetIndexValue{
		ID:    res.ID,
		Index: 2,
	}), dummy)
	if err != nil {
		t.Fatalf("oc.SetIndex: %s", err.Error())
	}

	t.Log("asd")
	if op.Index() != 2 {
		t.Fatalf("controller.SetIndex doesn't work: want: 2, have: %d", op.Index())
	}
	t.Log("asdfa")

	t.Log("asdfsa")
	<-closed
}
