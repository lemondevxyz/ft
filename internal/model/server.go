package model

type Server interface {
	Start() error
	IsRunning() bool
	Stop() error
}
