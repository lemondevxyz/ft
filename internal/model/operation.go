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

var opDelay = time.Millisecond * 100

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

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

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
	err    chan OperationError
	errWg  sync.WaitGroup
	exit   chan struct{}
	// src fields
	// srcMtx is used whenever src or srcIndex is going to be modified
	src sorcerer
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

	baseFs := afero.NewBasePathFs(fs, base)
	for i := range collect {
		collect[i].Fs = baseFs
		collect[i].AbsPath = path.Join(base, collect[i].Path)
		//oldpath := collect[i].Path
		//collect[i].Path = path.Join(path.Dir(base), collect[i].Path)
		//collect[i].basePath = base
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
		err:  make(chan OperationError),
		exit: make(chan struct{}),
		dst:  dst,
		src: sorcerer{
			index: -1,
			slice: src,
		},
		logger: &logger{log: distillog.NewNullLogger("")}}

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
	o.logger.lock()
	o.logger.log = l
	o.logger.unlock()
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
	o.mtx.RLock()
	if o.status != Default {
		o.mtx.RUnlock()
		return fmt.Errorf("cannot start an operation that has either already started, is resumed, has finished, or has been aborted")
	}
	o.mtx.RUnlock()

	o.logger.Infoln("Started the operation")
	o.mtx.Lock()
	o.status = Started
	o.mtx.Unlock()
	go o.do()

	return nil
}

// Exit exits out of the operation. Exit can happen while writing to
// a file, or while preparing to write to a file. If the file is being
// written to and Exit is called, then the file gets deleted afterwards.
//
// Exit makes an operation obsolete.
func (o *Operation) Exit() error {
	o.mtx.RLock()
	if o.status != Started && o.status != Paused {
		o.mtx.RUnlock()
		return fmt.Errorf("cannot exit out of an operation that has finished, hasn't started, or has been aborted")
	}
	o.mtx.RUnlock()

	o.logger.Infoln("Aborted the operation")
	o.mtx.Lock()
	o.status = Aborted
	o.mtx.Unlock()
	o.logger.Debugln("Closing the exit channel")
	close(o.exit)
	close(o.err)

	return nil
}

// Pause pauses the operation temporarily.
func (o *Operation) Pause() error {
	o.mtx.RLock()
	if o.status == Started {
		o.mtx.RUnlock()
		o.mtx.Lock()
		o.status = Paused
		o.mtx.Unlock()
		o.logger.Infoln("Paused the operation")
		return nil
	}
	o.mtx.RUnlock()

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
	o.mtx.RLock()
	if o.status == Paused {
		o.mtx.RUnlock()
		o.mtx.Lock()
		o.status = Started
		o.mtx.Unlock()
		o.logger.Infoln("Resumed the operation")
		return nil
	}
	o.mtx.RUnlock()

	return fmt.Errorf("cannot resume a non-paused operation")
}

// Proceed is used whenever an error is called. When an operation error
// occurs, the operation gets stuck unless Proceed is called.
func (o *Operation) Proceed() {
	defer func() {
		if recover() != nil {
			o.errWg.Add(1)
		}
	}()

	o.logger.Infoln("Proceeded with the error")
	o.errWg.Done()
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

// SetIndex sets the index for the collection. Can be used to skip a file
// whilst it is being written to.
func (o *Operation) SetIndex(n int) {
	o.logger.Debugf("SetIndex: %d", n)
	o.src.setIndex(n)
	o.logger.Debugf("SetIndex: len: %d", len(o.src.getSlice()))
}

// do is the main loop for operation, it handles all file transfers
// starting from index. do also adapts to any new files in the collection.
func (o *Operation) do() {
	o.logger.Infoln("do()")

	o.once.Do(func() {
		o.src.setIndex(0)
		for {
			arr := o.src.getSlice()
			index := o.src.getIndex()

			o.logger.Debugf("do(): loop: %d, %d\n", index, len(arr))
			if len(arr) <= index {
				o.logger.Debugln("do(): breaking...")
				break
			}

			select {
			case <-o.exit:
				o.logger.Debugln("do(): <-o.exit")
				o.mtx.RLock()
				if o.status != Aborted && o.status != Finished {
					o.logger.Debugln("Status != Aborted|Finished")
					o.mtx.Lock()
					o.status = Aborted
					o.mtx.Unlock()

					close(o.err)
					close(o.exit)
				}
				o.mtx.RUnlock()

				o.logger.Debugln("do(): returning from <-o.exit")
				return
			default:
				o.mtx.RLock()
				if o.status == Paused {
					o.mtx.RUnlock()
					o.logger.Infoln("do(): paused")
					time.Sleep(opDelay)
					continue
				} else {
					o.mtx.RUnlock()
				}

				srcFile := arr[index]
				o.logger.Debugf("do(): srcFile: %d, %s\n", index, srcFile.File.Name())

				o.logger.Debugln("do(): waiting")
				o.errWg.Wait()
				time.Sleep(opDelay)

				o.logger.Debugln(o.src.getIndex(), index)
				if o.src.getIndex() != index {
					o.logger.Debugln("do(): continue because index has been updated")
					continue
				}

				o.logger.Debugln("do(): done waiting")
				o.errWg.Add(1)

				errObj := OperationError{Src: srcFile, Dst: o.dst}
				errOut := func(err error) {
					errObj.Index = o.src.getIndex()
					o.logger.Debugf("do(): errOut: %s\n", err)
					errObj.Error = err
					o.mtx.RLock()
					if o.status != Aborted {
						o.err <- errObj
					}
					o.mtx.RUnlock()
				}

				dirPath := path.Clean(path.Dir(srcFile.Path))
				if len(dirPath) > 0 {
					err := o.dst.MkdirAll(dirPath, 0755)
					if err != nil {
						errOut(fmt.Errorf("%w: %s", ErrMkdir, err.Error()))
						continue
					}
				}

				_, err := o.dst.Stat(srcFile.Path)
				if err == nil {
					errOut(ErrDstAlreadyExists)
					continue
				}

				dstWriter, err := o.dst.OpenFile(srcFile.Path, os.O_WRONLY|os.O_CREATE, 0755)
				if err != nil {
					errOut(fmt.Errorf("%w: %s", ErrDstFile, err.Error()))
					continue
				}

				srcReader, err := srcFile.Fs.Open(srcFile.Path)
				if err != nil {
					errOut(fmt.Errorf("%w: %s", ErrSrcFile, err.Error()))
					continue
				}

				var size int64 = 0
				reader := shapeio.NewReader(srcReader)
				o.logger.Debugln("starting io.Copy")
				_, err = io.Copy(progressWriter(func(p []byte) (int, error) {
					n, err := dstWriter.Write(p)

					if o.opProgress != nil {
						// allow changing of size at writing speed levels
						size += int64(n)
						o.opProgress.Set(index, int64(size))
					}

					return n, err
				}), readerFunc(func(p []byte) (int, error) {
					select {
					case <-o.exit:
						return 0, ErrCancelled
					default:
						if o.src.getIndex() != index {
							return 0, ErrSkipFile
						}

						for {
							o.mtx.RLock()
							if o.status != Paused {
								o.mtx.RUnlock()
								break
							}

							if o.src.getIndex() != index {
								o.mtx.RUnlock()
								return 0, ErrSkipFile
							}
							o.mtx.RUnlock()

							time.Sleep(opDelay)
						}

						o.rateLimitMtx.Lock()
						reader.SetRateLimit(o.rateLimit)
						o.rateLimitMtx.Unlock()

						return reader.Read(p)
					}
				}))
				dstWriter.Close()
				srcReader.Close()
				if err != nil {
					srcFile.Fs.RemoveAll(srcFile.Path)
				}

				if err != nil && err != ErrSkipFile {
					errOut(fmt.Errorf("io.Copy: %w", err))
					continue
				}

				o.logger.Infof("do(): done transfer: %d, %d, %s\n", index, len(arr), srcFile.File.Name())
				o.errWg.Done()
				o.logger.Debugln("do(): sending empty error")
				o.mtx.RLock()
				if o.status != Aborted && o.status != Finished {
					o.err <- errObj
				}
				o.mtx.RUnlock()
				o.logger.Debugln("do(): done")

				if err == nil {
					o.src.setIndex(o.src.index + 1)
				}
			}

			time.Sleep(opDelay)
		}

		o.logger.Infoln("do(): loop done")
		o.mtx.RLock()
		status := o.status
		o.mtx.RUnlock()

		if status != Aborted {
			o.mtx.Lock()
			o.status = Finished
			o.mtx.Unlock()
			o.logger.Debugln("do(): closing o.err")
			close(o.err)
			o.logger.Debugln("do(): closing o.exit")
			close(o.exit)
		}
	})
}

type PublicOperation struct {
	*Operation
	Destination string `json:"dst"`
}

func (po PublicOperation) Map() map[string]interface{} {
	return map[string]interface{}{
		"index":  po.Index(),
		"src":    po.src.getSlice(),
		"status": po.status,
		"dst":    po.Destination,
	}
}

func (po PublicOperation) MarshalJSON() ([]byte, error) {
	return json.Marshal(po.Map())
}
