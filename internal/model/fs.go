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
	Path         string
	AbsolutePath string
}

func NewOsFileInfo(o os.FileInfo, path, abs string) OsFileInfo {
	return OsFileInfo{o, path, abs}
}

func (o OsFileInfo) Map() map[string]interface{} {
	return map[string]interface{}{
		"name":    o.Name(),
		"size":    o.Size(),
		"mode":    o.Mode(),
		"modTime": o.ModTime(),
		"path":    o.Path,
		"absPath": o.AbsolutePath,
	}
}

func (o OsFileInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Map())
}
