package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/gin-contrib/sse"
	"github.com/lemondevxyz/ft/internal/model"
	"github.com/spf13/afero"
	"github.com/thanhpk/randstr"
)

// Channel is a data structure that can collect an infinite amount of
// subscribers with the intent of sending events to each subscriber via
// its functions.
//
// An empty Channel is ready to use.
type Channel struct {
	mtx sync.Mutex
	m   map[string]chan sse.Event
}

var subscribers = map[string]chan sse.Event{}

func (c *Channel) init() {
	c.mtx.Lock()
	if c.m == nil {
		c.m = map[string]chan sse.Event{}
	}
	c.mtx.Unlock()
}

// Subscribe returns an id and a channel that sends when Announce gets
// called.
func (c *Channel) Subscribe() (string, chan sse.Event) {
	c.init()
	id := randstr.String(16)

	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.m[id] = make(chan sse.Event)

	return id, c.m[id]
}

// GetSubscriber returns a subscriber with that id or nil
func (c *Channel) GetSubscriber(id string) chan sse.Event {
	c.init()
	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.m[id]
}

// Unsubscribe removes the writer from the list of subscribers,
// thereby removing any effect Announce has.
func (c *Channel) Unsubscribe(id string) {
	c.init()
	c.mtx.Lock()
	delete(c.m, id)
	c.mtx.Unlock()
}

// Announce sends an event to all subscribers.
func (c *Channel) Announce(s sse.Event) {
	c.init()
	c.mtx.Lock()
	for _, v := range c.m {
		v <- s
	}
	c.mtx.Unlock()
}

var Subscribers = Channel{}

// DecodeOrFail tries to decode val to ctrl and if it fails, it writes
// and returns an error.
func DecodeOrFail(rd io.Reader, ctrl model.Controller, val interface{}) error {
	dec := json.NewDecoder(rd)
	err := dec.Decode(val)

	if err != nil {
		err = fmt.Errorf("json.Decoder: %w", err)
		ctrl.Error(model.ControllerError{
			ID:     "malformed-json",
			Reason: err.Error(),
		})

		return err
	}

	return nil
}

// StatOrFail tries to stat a file in fs by it's path and if it fails,
// it writes and returns an error.
func StatOrFail(ctrl model.Controller, fs afero.Fs, path string) error {
	if _, err := fs.Stat(path); err != nil {
		err = fmt.Errorf("file '%s' doesn't exist: %w", path, err)
		ctrl.Error(model.ControllerError{
			ID:     "file-stat-error",
			Reason: err.Error(),
		})
		return err
	}

	return nil
}

type operation struct {
	op    *model.Operation
	dst   string
	id    string
	owner string `json:"-"`
}

func (o *operation) Operation() *Operation {
	return &Operation{
		ID:   o.id,
		Srcs: model.CollectionToOperationFile(o.op.Sources()),
		Dst:  o.dst,
	}
}

type Operation struct {
	ID   string                `json:"id"`
	Srcs []model.OperationFile `json:"srcs"`
	Dst  string                `json:"dst"`
}

type OperationController struct {
	fs            afero.Fs
	operations    map[string]*operation
	operationsMtx sync.Mutex
	channel       *Channel
}

func NewOperationController(ch *Channel, fs afero.Fs) *OperationController {
	return &OperationController{fs: fs, operations: map[string]*operation{}, channel: ch}
}

func (oc *OperationController) AddOperation(op *model.Operation, dst, owner string) (string, error) {
	if oc.channel.GetSubscriber(owner) == nil {
		err := fmt.Errorf("no writer by that id %s", owner)
		return "", err
	}

	id := randstr.String(16)

	oc.operationsMtx.Lock()
	oc.operations[id] = &operation{op, dst, owner, id}
	oc.operationsMtx.Unlock()

	return id, nil
}

type NewOperationData struct {
	WriterID string `json:"writer_id"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
}

type NewOperationResult struct {
	ID string `json:"id"`
}

// NewOperations reads from the reader and writes the response
// to the controller.
func (oc *OperationController) NewOperation(rd io.Reader, ctrl model.Controller) (*NewOperationResult, error) {
	strct := &NewOperationData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return nil, err
	}

	if err := StatOrFail(ctrl, oc.fs, strct.Src); err != nil {
		return nil, err
	}

	if err := StatOrFail(ctrl, oc.fs, strct.Dst); err != nil {
		return nil, err
	}

	if strct.Src == strct.Dst {
		ctrl.Error(model.ControllerError{
			ID:     "same-src-dst",
			Reason: "source and destination are the same",
		})
		return nil, fmt.Errorf("source and destination are the same")
	}

	srcFs := afero.NewBasePathFs(oc.fs, strct.Src)
	dstFs := afero.NewBasePathFs(oc.fs, strct.Dst)

	oper, err := model.NewOperation(model.FsToCollection(srcFs), dstFs)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "model/operation-error",
			Reason: err.Error(),
		})
		return nil, err
	}

	id, err := oc.AddOperation(oper, strct.Dst, strct.WriterID)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "controller/operation-error",
			Reason: err.Error(),
		})
		return nil, err
	}

	res := NewOperationResult{id}
	ctrl.Value(res)

	o, _ := oc.GetOperationOrFail(nil, id)

	go oc.channel.Announce(sse.Event{
		Event: "event",
		Data:  o.Operation(),
	})

	return &res, nil
}

// GetOperation returns operation by its id or an error
func (oc *OperationController) GetOperation(id string) (*operation, error) {
	oc.operationsMtx.Lock()
	val, ok := oc.operations[id]
	oc.operationsMtx.Unlock()

	if !ok {
		return nil, fmt.Errorf("operation '%s' doesn't exist", id)
	}

	return val, nil
}

// GetOperationOrFail returns an operation, or if it doesn't exist it
// writes nil and error. Errors are also written to ctrl.
func (oc *OperationController) GetOperationOrFail(ctrl model.Controller, id string) (*operation, error) {
	op, err := oc.GetOperation(id)
	if err != nil {
		ctrl.Error(model.ControllerError{
			ID:     "operation-error",
			Reason: err.Error(),
		})
		return nil, err
	}

	return op, nil
}

type AddData struct {
	ID   string   `json:"id"`
	Srcs []string `json:"srcs"`
}

type AddValue AddData

// Add adds extra sources to the operation
func (oc *OperationController) SetSources(rd io.Reader, ctrl model.Controller) {
	strct := &AddData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return
	}

	collect := model.Collection{}
	for _, src := range strct.Srcs {
		if err := StatOrFail(ctrl, oc.fs, src); err != nil {
			return
		}

		collect = append(collect, model.FsToCollection(afero.NewBasePathFs(oc.fs, src))...)
	}

	val, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return
	}

	val.op.SetSources(collect)
	ctrl.Value(strct)
}

type GenericData struct {
	ID string `json:"id"`
}

type GenericValue GenericData

// Generic methods

func (oc *OperationController) Pause(rd io.Reader, ctrl model.Controller) error {
	strct := &GenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.op.Pause()
	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Resume(rd io.Reader, ctrl model.Controller) error {
	strct := &GenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.op.Resume()
	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Start(rd io.Reader, ctrl model.Controller) error {
	strct := &GenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.op.Start()
	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Exit(rd io.Reader, ctrl model.Controller) error {
	strct := &GenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.op.Exit()

	oc.operationsMtx.Lock()
	delete(oc.operations, strct.ID)
	oc.operationsMtx.Unlock()

	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Proceed(rd io.Reader, ctrl model.Controller) error {
	strct := &GenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.op.Proceed()
	ctrl.Value(strct)

	return nil
}
