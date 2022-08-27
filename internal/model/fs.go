package model

import (
	"github.com/spf13/afero"
)

type Fs interface {
	afero.Fs
	IsMounted() error
	Mount() error
	Unmount() error
}
