package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ControllerError struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type Controller interface {
	// Error is a function that tells the client that their request
	// encountered an error.
	Error(err ControllerError)
	// Value is a function that tells the client that their request
	// was successful with an extra value.
	Value(val interface{}) error
}

type DummyController struct {
	buf *bytes.Buffer
	enc *json.Encoder
	dec *json.Decoder
	err *ControllerError
}

func (d *DummyController) Error(err ControllerError) {
	d.err = &err
}

func (d *DummyController) Value(val interface{}) error {
	if d.err != nil {
		return fmt.Errorf("please read error via GetError()")
	}

	if d.buf == nil {
		d.buf = bytes.NewBuffer(nil)
	}

	if d.enc == nil {
		d.enc = json.NewEncoder(d.buf)
	}

	return d.enc.Encode(val)
}

func (d *DummyController) GetError() *ControllerError {
	err := d.err
	if err != nil {
		d.err = nil
		return err
	}

	return nil
}

func (d *DummyController) Read(v interface{}) error {
	if d.buf == nil {
		d.buf = bytes.NewBuffer(nil)
	}

	if d.dec == nil {
		d.dec = json.NewDecoder(d.buf)
	}

	return d.dec.Decode(v)
}
