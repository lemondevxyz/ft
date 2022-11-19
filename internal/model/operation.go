package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sync"
	"time"

	"github.com/amoghe/distillog"
	"github.com/fujiwara/shapeio"
	"github.com/spf13/afero"
)

const opPauseDelay = time.Millisecond

// ProgressSetter is an interface that allows for progress setting for
// files. Whenever a file gets written to in *Operation, ProgressSetter's
// Set function is called.
//
// In conjuction with index and src, ProgressSetter can provide real
// time progress of the file transfers.
type ProgressSetter interface {
	// Set is a function that provides real-time updates (writes) for clients.
	// Set must be safe for concurrent use.
	Set(index int, written int64)
}

type progressWriter func(p []byte) (n int, err error)

func (pw progressWriter) Write(p []byte) (n int, err error) { return pw(p) }

type FileInfo struct {
	Fs      afero.Fs
	File    os.FileInfo
	Path    string
	AbsPath string
}

func (f FileInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(NewOsFileInfo(f.File, f.Path, f.AbsPath))
}

// OperationFile is a marshallable object that is used to communicate
// with Controller.
type OperationFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64
	ModTime time.Time
	IsDir   bool
}

type JSONError struct {
	Err error
}

func (j *JSONError) MarshalJSON() ([]byte, error) {
	if j == nil {
		return nil, errors.New("nil pointer")
	}

	lastErr := j.Err
	for {
		err := errors.Unwrap(lastErr)
		if err == nil {
			break
		}

		lastErr = err
	}

	return json.Marshal(lastErr.Error())
}

// OperationError is an error that can occur in the middle of an
// operation.
type OperationError struct {
	Index int
	Src   FileInfo
	Dst   afero.Fs
	Error error
}

// Operation is an object that contains a file transfer from multiple
// sources to one destination. An operation can be paused, resumed, or
// cancelled(Exit).
//
// Operations typically endure errors but through Operation.Error these
// errors can be dynamically handled by an outsider package so that
// Operation is extensible via other packages.
type Operation struct {
	// mtx field represents the mtx for the whole Operation
	// mtx is locked whenever the status has been changed
	// or during do at the start
	mtx    sync.RWMutex
	status uint8
	once   sync.Once
	// errSync is only a field because new clients should have the ability
	// to see a previous error.
	errSync *OperationError
	err     chan OperationError
	errWg   sync.WaitGroup
	// src fields
	// srcMtx is used whenever src or srcIndex is going to be modified
	src *sorcerer
	// the destination
	dst afero.Fs
	// progress fields
	opProgress ProgressSetter
	// logger
	logger *logger
	// rateLimit
	rateLimitMtx sync.Mutex
	rateLimit    float64
}

var (
	ErrCancelled        = errors.New("cancelled copy")
	ErrMkdir            = errors.New("mkdir error")
	ErrDstFile          = errors.New("dst file")
	ErrDstAlreadyExists = errors.New("dst file already exists")
	ErrSrcFile          = errors.New("src file")
	ErrSkipFile         = errors.New("skip file")
)

// FsToCollection takes in an afero file system and turns it into a collection
// of files. The collection is always recursive.
func FsToCollection(localfs afero.Fs) (Collection, error) {
	arr := Collection{}
	err := afero.Walk(localfs, ".", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			arr = append(arr, FileInfo{
				Fs:   localfs,
				File: info,
				Path: path,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return arr, nil
}

// DirToCollection gets all files within a directory and adds them to a
// collection.
func DirToCollection(fs afero.Fs, base string) (Collection, error) {
	collect, err := FsToCollection(afero.NewBasePathFs(fs, base))
	if err != nil {
		return nil, err
	}

	var baseFs afero.Fs
	if path.Dir(base) == "." {
		baseFs = fs
	} else {
		baseFs = afero.NewBasePathFs(fs, path.Dir(base))
	}

	for i := range collect {
		collect[i].Fs = baseFs
		collect[i].AbsPath = path.Join(base, collect[i].Path)
		collect[i].Path = path.Join(path.Base(base), collect[i].Path)
	}

	return collect, nil
}

type Collection []FileInfo

type OperationProgress struct {
	m        sync.Mutex
	progress map[int]int64
}

const (
	Default uint8 = iota
	Started
	Finished
	Aborted
	Paused
)

// NewOperation returns an operation object.
func NewOperation(src Collection, dst afero.Fs) (*Operation, error) {
	op := &Operation{
		err: make(chan OperationError),
		dst: dst,
		src: &sorcerer{
			index: -1,
			slice: src,
		},
		logger: &logger{log: distillog.NewNullLogger("")},
	}

	if len(src) == 0 {
		return nil, fmt.Errorf("src cannot be empty")
	}

	for i, v := range src {
		if v.File == nil || v.Fs == nil || len(v.Path) == 0 {
			return nil, fmt.Errorf("bad file %d %v", i, v)
		}
	}

	return op, nil
}

// SetRateLimit sets a rate limit for how fast can a reader be read.
func (o *Operation) SetRateLimit(val float64) {
	o.rateLimitMtx.Lock()
	o.rateLimit = val
	o.rateLimitMtx.Unlock()
}

// SetLogger sets the logger for the Operation
func (o *Operation) SetLogger(l distillog.Logger) {
	o.logger.setLogger(l)
}

// SetProgress sets the progress setter for the files.
func (o *Operation) SetProgress(v ProgressSetter) {
	o.logger.Debugf("SetProgress: %v", v)
	o.opProgress = v
}

// Status returns the operation's status
func (o *Operation) Status() uint8 {
	o.mtx.RLock()
	defer o.mtx.RUnlock()

	return o.status
}

// Destination returns the destination file system. Please note that
// destinations should not be changed, instead create a new operation.
func (o *Operation) Destination() afero.Fs {
	return o.dst
}

// Start starts the operation
func (o *Operation) Start() error {
	if o.Status() != Default {
		return fmt.Errorf("cannot start an operation that has either already started, is resumed, has finished, or has been aborted")
	}

	o.logger.Infoln("Started the operation")
	o.setStatus(Started)
	go o.once.Do(o.do)

	return nil
}

// Exit exits out of the operation. Exit can happen while writing to
// a file, or while preparing to write to a file. If the file is being
// written to and Exit is called, then the file gets deleted afterwards.
//
// Exit makes an operation obsolete.
func (o *Operation) Exit() error {
	status := o.Status()
	if status != Started && status != Paused {
		return fmt.Errorf("cannot exit out of an operation that has finished, hasn't started, or has been aborted")
	}

	o.logger.Infoln("Aborted the operation")
	o.setStatus(Aborted)
	o.logger.Debugln("Closing the exit channel")
	o.closeChannels()

	return nil
}

// Pause pauses the operation temporarily.
func (o *Operation) Pause() error {
	if o.Status() == Started {
		o.setStatus(Paused)
		o.logger.Infoln("Paused the operation")
		return nil
	}

	return fmt.Errorf("cannot pause a non-started operation")
}

// Size returns the src size of the operation. Do note that this function
// is rather expensive as it contains tons of syscalls. Prefer to send a
// cached value and calling the function only when the sources have changed.
func (o *Operation) Size() int64 {
	var size int64 = 0
	src := o.src.getSlice()
	for _, v := range src {
		size += v.File.Size()
	}

	return size
}

// Resume resumes the operation.
func (o *Operation) Resume() error {
	if o.getStatus() == Paused {
		o.mtx.Lock()
		o.errSync = nil
		o.status = Started
		o.mtx.Unlock()
		o.logger.Infoln("Resumed the operation")
		return nil
	}

	return fmt.Errorf("cannot resume a non-paused operation")
}

// Error returns the error if there is any, or hangs if there isn't an
// error.
func (o *Operation) Error() OperationError {
	o.logger.Debugln("Error(): waiting for channel recv")
	err := <-o.err
	o.logger.Debugln("Error(): done channel recv")
	return err
}

// Sources returns the list of files that are to be copied ot the destination.
func (o *Operation) Sources() Collection {
	return o.src.getSlice()
}

// Index returns the index of the current file
func (o *Operation) Index() int {
	return o.src.getIndex()
}

// SetSources sets the sources for the operation.
func (o *Operation) SetSources(c Collection) {
	o.src.setSlice(c)
}

func (o *Operation) closeChannels() {
	o.mtx.Lock()
	if o.err != nil {
		close(o.err)
	}
	o.err = nil
	o.mtx.Unlock()
}

// SetIndex sets the index for the collection. Can be used to skip a file
// whilst it is being written to.
func (o *Operation) SetIndex(n int) {
	o.logger.Debugf("SetIndex: %d", n)
	o.src.setIndex(n)
	o.logger.Debugf("SetIndex: len: %d", len(o.src.getSlice()))
}

func (o *Operation) setStatus(status uint8) {
	o.mtx.Lock()
	o.status = status
	o.mtx.Unlock()
}

func (o *Operation) getStatus() uint8 {
	o.mtx.RLock()
	ret := o.status
	o.mtx.RUnlock()

	return ret
}

func (o *Operation) errOut(errObj OperationError, err error) {
	errObj.Index = o.src.getIndex()
	o.logger.Debugf("errOut: %s\n", err)
	errObj.Error = err

	o.mtx.Lock()
	ch := o.err
	o.mtx.Unlock()

	if ch != nil {
		o.Pause()
		o.errSync = &errObj
		o.err <- errObj
		o.logger.Debugf("errOut: sent the error: %s\n", err)
	}
}

func (o *Operation) RateLimit() float64 {
	o.rateLimitMtx.Lock()
	defer o.rateLimitMtx.Unlock()

	return o.rateLimit
}

// do is the main loop for operation, it handles all file transfers
// starting from index. do also adapts to any new files in the collection.
func (o *Operation) do() {
	o.logger.Infoln("do()")
	o.src.setIndex(0)

	for {
		arr := o.src.getSlice()
		index := o.src.getIndex()

		o.logger.Debugf("do(): loop: %d, %d\n", index, len(arr))
		if len(arr) <= index {
			break
		}

		srcFile := arr[index]

		o.logger.Debugf("do(): srcFile: %d, %s\n", index, srcFile.File.Name())
		o.logger.Debugln("do(): waiting")

		for o.getStatus() == Paused {
			time.Sleep(opPauseDelay)
		}

		if o.getStatus() == Aborted {
			o.logger.Debugln("do(): status == aborted")
			o.closeChannels()
			return
		}

		errObj := OperationError{Src: srcFile, Dst: o.dst}
		errOut := func(err error) {
			o.errOut(errObj, err)
			o.logger.Debugf("do(): error out: %v", errObj)
		}

		o.logger.Debugln(o.src.getIndex(), index)
		if o.src.getIndex() != index {
			o.logger.Debugln("do(): skipping file")

			errOut(ErrSkipFile)
			continue
		}

		o.logger.Debugln("do(): done waiting")

		if err := mkdirIfNeeded(o.dst, srcFile.Path); err != nil {
			errOut(fmt.Errorf("mkdirIfNeeded: %w", err))
			continue
		}

		srcReader, dstWriter, err := openSrcAndDir(srcFile.Fs, o.dst, srcFile.Path)
		if err != nil {
			errOut(fmt.Errorf("openSrcAndDir: %w", err))
			continue
		}

		o.logger.Debugln("do(): starting io.Copy")

		n, err := io.Copy(o.operationWriter(dstWriter, index), o.operationReader(srcReader, index))
		if o.opProgress != nil {
			o.opProgress.Set(index, n)
		}

		closeAll(srcReader, dstWriter)
		removeIfErrNotNil(err, FileInfo{
			Fs:   o.dst,
			Path: srcFile.Path,
		})

		if err != nil && err != ErrSkipFile {
			errOut(err)
			continue
		}

		o.logger.Infof("do(): done transfer: %d, %d, %s\n", index, len(arr), srcFile.File.Name())
		if o.getStatus() != Aborted {
			o.logger.Debugln("do(): sending empty error")
			o.mtx.Lock()
			if o.err != nil {
				o.errSync = &errObj
				o.err <- errObj
			}
			o.mtx.Unlock()
		}

		o.logger.Debugln("do(): done")

		newIndex := o.src.getIndex() + 1
		o.src.setIndex(newIndex)
	}

	o.logger.Infoln("do(): loop done")

	o.setStatus(Finished)
	o.closeChannels()
}

func (o *Operation) operationReader(srcReader io.Reader, index int) *operationReader {
	return &operationReader{
		getRateLimit: o.RateLimit,
		getIndex:     o.src.getIndex,
		getStatus:    o.getStatus,
		reader:       shapeio.NewReader(srcReader),
		cachedIndex:  index,
	}
}

func (o *Operation) operationWriter(writer io.Writer, index int) *operationWriter {
	return &operationWriter{
		size:     0,
		progress: o.opProgress,
		index:    index,
		writer:   writer,
	}
}

type PublicOperation struct {
	*Operation
	Destination string `json:"dst"`
}

func (po PublicOperation) Map() map[string]interface{} {
	m := map[string]interface{}{
		"index":     po.Index(),
		"src":       po.src.getSlice(),
		"status":    po.status,
		"dst":       po.Destination,
		"rateLimit": po.RateLimit(),
	}

	if po.errSync != nil {
		m["err"] = map[string]interface{}{
			"index": po.errSync.Index,
			"src":   po.errSync.Src.File.Name(),
			"dst":   po.Destination,
			"error": &JSONError{po.errSync.Error},
		}
	}

	return m
}

func (po PublicOperation) MarshalJSON() ([]byte, error) {
	return json.Marshal(po.Map())
}

func mkdirIfNeeded(fs afero.Fs, pathval string) (err error) {
	dirPath := path.Clean(path.Dir(pathval))
	if len(dirPath) > 0 {
		err = fs.MkdirAll(dirPath, 0755)
	}

	return err
}

func openSrcAndDir(srcfs, dstfs afero.Fs, filepath string) (afero.File, afero.File, error) {
	_, err := dstfs.Stat(filepath)
	if err == nil {
		return nil, nil, ErrDstAlreadyExists
	}

	dstWriter, err := dstfs.OpenFile(filepath, os.O_WRONLY|os.O_CREATE, 0755)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrDstFile, err.Error())
	}

	srcReader, err := srcfs.OpenFile(filepath, os.O_RDONLY, 0755)
	if err != nil {
		dstWriter.Close()
		return nil, nil, fmt.Errorf("%w: %s", ErrSrcFile, err.Error())
	}

	return srcReader, dstWriter, nil
}

func closeAll(arr ...io.Closer) {
	for _, v := range arr {
		v.Close()
	}
}

func removeIfErrNotNil(err error, srcFile FileInfo) {
	if err != nil {
		srcFile.Fs.RemoveAll(srcFile.Path)
	}
}

type operationReader struct {
	reader      *shapeio.Reader
	getIndex    func() int
	cachedIndex int
	getStatus   func() uint8

	getRateLimit func() float64
}

func (rd *operationReader) Read(p []byte) (int, error) {

	if rd.getIndex() != rd.cachedIndex {
		return 0, ErrSkipFile
	} else if rd.getStatus() == Aborted {
		return 0, ErrCancelled
	}

	for rd.getStatus() == Paused {
		time.Sleep(opPauseDelay)
	}

	rd.reader.SetRateLimit(rd.getRateLimit())

	return rd.reader.Read(p)
}

type operationWriter struct {
	progress ProgressSetter
	size     int64
	writer   io.Writer
	index    int
}

func (wr *operationWriter) Write(p []byte) (int, error) {
	n, err := wr.writer.Write(p)

	if wr.progress != nil {
		// allow changing of size at writing speed levels
		wr.size += int64(n)
		wr.progress.Set(wr.index, int64(wr.size))
	}

	return n, err
}
