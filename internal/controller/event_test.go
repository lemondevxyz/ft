package controller

import (
	"fmt"
	"testing"

	"github.com/lemondevxyz/ft/internal/model"
	"github.com/matryer/is"
)

func TestEventFsRemove(t *testing.T) {
	is := is.New(t)
	ev := EventFsRemove("asd")

	is.Equal(ev.Event, "fs-remove")
	is.Equal(ev.Data, "asd")
}

func TestEventFsMkdir(t *testing.T) {
	is := is.New(t)
	ev := EventFsMkdir("asd")

	is.Equal(ev.Event, "fs-mkdir")
	is.Equal(ev.Data, "asd")
}

func TestEventFsMove(t *testing.T) {
	is := is.New(t)
	ev := EventFsMove("old", "new")

	is.Equal(ev.Event, "fs-move")
	strct := ev.Data.(struct {
		Old string `json:"old"`
		New string `json:"new"`
	})

	is.Equal(strct.Old, "old")
	is.Equal(strct.New, "new")
}

func TestEventOperationProgress(t *testing.T) {
	is := is.New(t)
	ev := EventOperationProgress("id", 0, 123)

	is.Equal(ev.Event, "operation-progress")
	strct := ev.Data.(struct {
		ID    string `json:"id"`
		Index int    `json:"index"`
		Size  int64  `json:"size"`
	})

	is.Equal(strct.ID, "id")
	is.Equal(strct.Index, 0)
	is.Equal(strct.Size, int64(123))
}

func TestEventOperationNewUpdate(t *testing.T) {
	is := is.New(t)

	op := &Operation{}
	{
		ev := EventOperationNew(op)

		is.Equal(ev.Event, "operation-new")
		is.Equal(ev.Data.(*Operation), op)
	}
	{
		ev := EventOperationUpdate(op)

		is.Equal(ev.Event, "operation-update")
		is.Equal(ev.Data.(*Operation), op)
	}
}

func TestEventOperationAll(t *testing.T) {
	is := is.New(t)

	o := map[string]*Operation{}

	ev := EventOperationAll(o)
	is.Equal(ev.Event, "operation-all")
	is.Equal(ev.Data.(map[string]*Operation), o)
}

func TestEventOperationDone(t *testing.T) {
	is := is.New(t)

	ev := EventOperationDone("asd")

	is.Equal(ev.Event, "operation-done")
	is.Equal(ev.Data, "asd")
}

func TestEventOperationStatus(t *testing.T) {
	is := is.New(t)

	ev := EventOperationStatus("id", 10)

	is.Equal(ev.Event, "operation-status")
	status := ev.Data.(struct {
		ID     string `json:"id"`
		Status uint8  `json:"status"`
	})

	is.Equal(status.ID, "id")
	is.Equal(status.Status, uint8(10))
}

func TestEventOperationError(t *testing.T) {
	is := is.New(t)

	ev := EventOperationError("id", "dst", model.OperationError{
		Src: model.FileInfo{AbsPath: "src"},
	})

	val := ev.Data.(struct {
		ID    string           `json:"id"`
		Src   string           `json:"src"`
		Dst   string           `json:"dst"`
		Error *model.JSONError `json:"error"`
		Index int              `json:"index"`
	})
	is.Equal(val.ID, "id")
	is.Equal(val.Dst, "dst")
	is.Equal(val.Src, "src")
	is.Equal(val.Error, nil)
	is.Equal(val.Index, 0)

	ev = EventOperationError("id", "dst", model.OperationError{
		Src:   model.FileInfo{AbsPath: "src"},
		Error: fmt.Errorf("error"),
	})
	val = ev.Data.(struct {
		ID    string           `json:"id"`
		Src   string           `json:"src"`
		Dst   string           `json:"dst"`
		Error *model.JSONError `json:"error"`
		Index int              `json:"index"`
	})

	is.Equal(val.Error.Err.Error(), "error")
}
