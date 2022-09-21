package controller

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/amoghe/distillog"
	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
)

func initFS() (afero.Fs, error) {
	afs := afero.NewMemMapFs()

	err := afs.MkdirAll("src/content/", 0755)
	if err != nil {
		return nil, err
	}

	fi, err := afs.OpenFile("src/ok.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return nil, err
	}
	fi.WriteString("hello")
	fi.Close()

	fi, err = afs.OpenFile("src/content/ok.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return nil, err
	}

	fi.WriteString("hello part 2")
	fi.Close()

	fi, err = afs.OpenFile("src/content/ok2.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return nil, err
	}
	fi.WriteString("Hello part 3")
	fi.Close()

	fi, err = afs.OpenFile("new.txt", os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return nil, err
	}
	fi.WriteString("extra file")
	fi.Close()

	err = afs.MkdirAll("dst", 0755)
	if err != nil {
		return nil, err
	}

	return afs, nil
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
	afs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

	channel := &Channel{}

	oc, err := NewOperationController(channel, afs)
	if err != nil {
		t.Fatalf("NewOperationController: %s", err.Error())
	}

	id, ch := channel.Subscribe()

	ev := make(chan sse.Event)
	go func() {
		val := <-ch
		ev <- val
	}()

	ctrl := &model.DummyController{}

	_, err = oc.NewOperation(encodeJSON(OperationNewData{
		WriterID: id,
		Src:      []string{"src"},
		Dst:      "dst",
	}), ctrl)
	if err != nil {
		t.Fatalf("couldn't start operation: %s", err.Error())
	}

	<-ev
	channel.Unsubscribe(id)
}

func TestOperationControllerGeneric(t *testing.T) {
	afs, err := initFS()
	if err != nil {
		t.Fatalf("initFS: %s", err.Error())
	}

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

	op.SetLogger(distillog.NewStderrLogger("testing"))
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
