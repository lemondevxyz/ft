package controller

import (
	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
)

func EventFsRemove(path string) sse.Event {
	return sse.Event{
		Event: "fs-remove",
		Data:  path,
	}
}

func EventFsMkdir(path string) sse.Event {
	return sse.Event{
		Event: "fs-mkdir",
		Data:  path,
	}
}

func EventFsMove(old, new string) sse.Event {
	return sse.Event{
		Event: "fs-move",
		Data: struct {
			Old string `json:"old"`
			New string `json:"new"`
		}{old, new},
	}
}

// Public methods that all subscribers get access to
func EventOperationProgress(id string, index int, size int64) sse.Event {
	return sse.Event{
		Event: "operation-progress",
		Data: struct {
			ID    string `json:"id"`
			Index int    `json:"index"`
			Size  int64  `json:"size"`
		}{id, index, size},
	}
}

func EventOperationNew(o *Operation) sse.Event {
	return sse.Event{
		Event: "operation-new",
		Data:  o,
	}
}

func EventOperationDone(id string) sse.Event {
	return sse.Event{
		Event: "operation-done",
		Data:  id,
	}
}

func EventOperationStatus(id string, status uint8) sse.Event {
	return sse.Event{
		Event: "operation-status",
		Data: struct {
			ID     string
			Status uint8
		}{id, status},
	}
}

// Methods that are only sent for the owner
func EventOperationError(id string, dst string, err model.OperationError) sse.Event {
	return sse.Event{
		Event: "operation-error",
		Data: struct {
			ID    string `json:"id"`
			Src   string `json:"src"`
			Dst   string `json:"dst"`
			Error string `json:"error"`
		}{id, err.Src.File.Name(), dst, err.Error.Error()},
	}
}
