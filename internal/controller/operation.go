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
	return id, c.SetSubscriber(id)
}

// GetSubscriber returns a subscriber with that id or nil
func (c *Channel) GetSubscriber(id string) chan sse.Event {
	c.init()
	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.m[id]
}

// SetSubscriber creates a subscriber and assign that particular id to
// it.
func (c *Channel) SetSubscriber(id string) chan sse.Event {
	c.init()

	c.mtx.Lock()
	defer c.mtx.Unlock()
	c.m[id] = make(chan sse.Event)

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

type Operation struct {
	*model.PublicOperation
	ID string
	//Owner string `json:"-"`
	mtx sync.Mutex
}

func (o *Operation) lock()   { o.mtx.Lock() }
func (o *Operation) unlock() { o.mtx.Unlock() }

func (o *Operation) MarshalJSON() ([]byte, error) {
	m := o.Map()

	m["id"] = o.ID

	return json.Marshal(m)
}

type ProgressBroadcaster struct {
	id      string
	channel *Channel
}

func (p *ProgressBroadcaster) Set(index int, size int64) {
	p.channel.Announce(EventOperationProgress(p.id, index, size))
}

type OperationController struct {
	fs            afero.Fs
	operations    map[string]*Operation
	operationsMtx sync.Mutex
	channel       *Channel
}

func NewOperationController(ch *Channel, fs afero.Fs) (*OperationController, error) {
	if ch == nil || fs == nil {
		return nil, fmt.Errorf("one or more of the parameters is nil")
	}

	return &OperationController{fs: fs, operations: map[string]*Operation{}, channel: ch}, nil
}

func (oc *OperationController) AddOperation(op *model.Operation, dst, owner string) (string, error) {
	if oc.channel.GetSubscriber(owner) == nil {
		err := fmt.Errorf("no writer by that id %s", owner)
		return "", err
	}

	id := randstr.String(16)

	oc.operationsMtx.Lock()
	oc.operations[id] = &Operation{&model.PublicOperation{Operation: op, Destination: dst}, owner, sync.Mutex{}}
	oc.operationsMtx.Unlock()

	return id, nil
}

type OperationNewData struct {
	WriterID string `json:"writer_id"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
}

type OperationNewResult struct {
	ID string `json:"id"`
}

// NewOperations reads from the reader and writes the response
// to the controller.
func (oc *OperationController) NewOperation(rd io.Reader, ctrl model.Controller) (*OperationNewResult, error) {
	strct := &OperationNewData{}
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

	res := OperationNewResult{id}
	ctrl.Value(res)

	o, _ := oc.GetOperationOrFail(nil, id)
	oper.SetProgress(&ProgressBroadcaster{
		id:      id,
		channel: oc.channel,
	})

	go oc.channel.Announce(EventOperationNew(*o))

	return &res, nil
}

// GetOperation returns operation by its id or an error
func (oc *OperationController) GetOperation(id string) (*Operation, error) {
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
func (oc *OperationController) GetOperationOrFail(ctrl model.Controller, id string) (*Operation, error) {
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

type OperationSetSourcesData struct {
	ID   string   `json:"id"`
	Srcs []string `json:"srcs"`
}

type OperationSetSourcesValue OperationSetSourcesData

// Add adds extra sources to the operation
func (oc *OperationController) SetSources(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationSetSourcesData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	collect := model.Collection{}
	for _, src := range strct.Srcs {
		if err := StatOrFail(ctrl, oc.fs, src); err != nil {
			return err
		}

		collect = append(collect, model.FsToCollection(afero.NewBasePathFs(oc.fs, src))...)
	}

	val, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	val.SetSources(collect)
	ctrl.Value(strct)

	return nil
}

type OperationGenericData struct {
	ID string `json:"id"`
}
type OperationGenericValue OperationGenericData

// Generic methods

func (oc *OperationController) Pause(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationGenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.Pause()
	go oc.channel.Announce(EventOperationStatus(op.ID, op.Status()))
	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Resume(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationGenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.Resume()
	go oc.channel.Announce(EventOperationStatus(op.ID, op.Status()))
	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Start(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationGenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.Start()
	go oc.channel.Announce(EventOperationStatus(op.ID, op.Status()))
	ctrl.Value(strct)

	go func() {
		for {
			err := op.Error()
			oc.channel.Announce(EventOperationError(op.ID, op.Destination, err))

			if op.Status() == model.Finished {
				oc.channel.Announce(EventOperationDone(op.ID))
				break
			}
		}
	}()

	return nil
}

func (oc *OperationController) Exit(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationGenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.Exit()

	oc.operationsMtx.Lock()
	delete(oc.operations, strct.ID)
	oc.operationsMtx.Unlock()

	ctrl.Value(strct)

	return nil
}

func (oc *OperationController) Proceed(rd io.Reader, ctrl model.Controller) error {
	strct := &OperationGenericData{}
	if err := DecodeOrFail(rd, ctrl, strct); err != nil {
		return err
	}

	op, err := oc.GetOperationOrFail(ctrl, strct.ID)
	if err != nil {
		return err
	}

	op.Proceed()
	ctrl.Value(strct)

	return nil
}
