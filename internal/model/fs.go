package model

import (
	"encoding/json"
	"os"

	"github.com/spf13/afero"
)

type Fs interface {
	afero.Fs
	IsMounted() error
	Mount() error
	Unmount() error
}

// OsFileInfo is a wrapper around os.FileInfo that allows it to be
// marshalled to json.
type OsFileInfo struct {
	os.FileInfo
}

func NewOsFileInfo(o os.FileInfo) OsFileInfo {
	return OsFileInfo{o}
}

func (o OsFileInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"name":    o.Name(),
		"size":    o.Size(),
		"mode":    o.Mode(),
		"modTime": o.ModTime(),
	})
}
