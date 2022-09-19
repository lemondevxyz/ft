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
	"github.com/spf13/afero"
)

// ProgressSetter is an interface that allows for progress setting for
// files. Whenever a file gets written to in *Operation, ProgressSetter's
// Set function is called.
//
// With conjuction of index and src, ProgressSetter can provide real
// time progress of the file transfers.
type ProgressSetter interface {
	Set(index int, written int64)
}

type progressWriter func(p []byte) (n int, err error)

func (pw progressWriter) Write(p []byte) (n int, err error) { return pw(p) }

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

type FileInfo struct {
	Fs   afero.Fs
	File os.FileInfo
	Path string
}

func (f FileInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(NewOsFileInfo(f.File, f.Path))
}

// OperationFile is a marshallable object that is used to communicate
// with Controller.
type OperationFile struct {
	Path    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

// CollectionToOperationFile turns a Collection to a slice of
// OperationFile.
func CollectionToOperationFile(c Collection) (arr []OperationFile) {
	for _, v := range c {
		arr = append(arr, OperationFile{
			Path:    v.Path,
			Size:    v.File.Size(),
			ModTime: v.File.ModTime(),
			IsDir:   v.File.IsDir(),
		})
	}

	return
}

// OperationError is an error that can occur in the middle of an
// operation.
type OperationError struct {
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
	mtx    sync.Mutex
	status uint8
	once   sync.Once
	err    chan OperationError
	errWg  sync.WaitGroup
	exit   chan struct{}
	// src fields
	// srcMtx is used whenever src or srcIndex is going to be modified
	srcMtx   sync.Mutex
	src      Collection
	srcIndex int
	// the destination
	dst afero.Fs
	// progress fields
	opProgress ProgressSetter
	// logger
	logger distillog.Logger
}

var (
	ErrCancelled        = errors.New("cancelled copy")
	ErrMkdir            = errors.New("mkdir error")
	ErrDstFile          = errors.New("dst file")
	ErrDstAlreadyExists = errors.New("dst file already exists")
	ErrSrcFile          = errors.New("src file")
)

// FsToCollection takes in an afero file system and turns it into a collection
// of files. The collection is always recursive.
func FsToCollection(localfs afero.Fs) Collection {
	arr := Collection{}
	afero.Walk(localfs, ".", func(path string, info fs.FileInfo, err error) error {
		if !info.IsDir() {
			arr = append(arr, FileInfo{
				localfs,
				info,
				path,
			})
		}

		return nil
	})

	return arr
}

func (o *Operation) lock()   { o.mtx.Lock() }
func (o *Operation) unlock() { o.mtx.Unlock() }

func (o *Operation) srcLock() {
	o.srcMtx.Lock()
	o.logger.Infoln("srcLock()")
}

func (o *Operation) srcUnlock() {
	o.srcMtx.Unlock()
	o.logger.Infoln("srcUnlock()")
}

func (o *Operation) infof(fmt string, v ...interface{}) {
	o.lock()
	o.logger.Infof(fmt, v...)
	o.unlock()
}

func (o *Operation) infoln(v ...interface{}) {
	o.lock()
	o.logger.Infoln(v...)
	o.unlock()
}

func (o *Operation) errorf(fmt string, v ...interface{}) {
	o.lock()
	o.logger.Errorf(fmt, v...)
	o.unlock()
}

func (o *Operation) errorln(v ...interface{}) {
	o.lock()
	o.logger.Errorln(v...)
	o.unlock()
}

type Collection []FileInfo

func (c Collection) MarshalJSON() ([]byte, error) {
	return json.Marshal(CollectionToOperationFile(c))
}

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
		err:      make(chan OperationError),
		exit:     make(chan struct{}),
		src:      src,
		dst:      dst,
		srcIndex: -1,
		logger:   distillog.NewNullLogger("")}

	return op, nil
}

// SetLogger sets the logger for the Operation
func (o *Operation) SetLogger(l distillog.Logger) {
	o.lock()
	defer o.unlock()
	o.logger = l
}

// SetProgress sets the progress setter for the files.
func (o *Operation) SetProgress(v ProgressSetter) {
	o.lock()
	defer o.unlock()
	o.logger.Infof("SetProgress: %v", v)
	o.opProgress = v
}

// Status returns the operation's status
func (o *Operation) Status() uint8 {
	o.lock()
	defer o.unlock()

	return o.status
}

// Destination returns the destination file system. Please note that
// destinations should not be changed, instead create a new operation.
func (o *Operation) Destination() afero.Fs {
	return o.dst
}

// Start starts the operation
func (o *Operation) Start() error {
	defer o.unlock()
	o.lock()
	if o.status != Default {
		return fmt.Errorf("cannot start an operation that has either already started, is resumed, has finished, or has been aborted")
	}

	o.logger.Infoln("Started the operation")
	o.status = Started
	go o.do()

	return nil
}

// Exit exits out of the operation. Exit can happen while writing to
// a file, or while preparing to write to a file. If the file is being
// written to and Exit is called, then the file gets deleted afterwards.
//
// Exit makes an operation obsolete.
func (o *Operation) Exit() error {
	defer o.unlock()
	o.lock()
	if o.status != Started && o.status != Paused {
		return fmt.Errorf("cannot exit out of an operation that has finished, hasn't started, or has been aborted")
	}

	o.logger.Infoln("Aborted the operation")
	o.status = Aborted
	o.logger.Infoln("Closing the exit channel")
	close(o.exit)
	close(o.err)

	return nil
}

// Pause pauses the operation temporarily.
func (o *Operation) Pause() error {
	defer o.unlock()
	o.lock()

	if o.status == Started {
		o.status = Paused
		o.logger.Infoln("Paused the operation")
		return nil
	}

	return fmt.Errorf("cannot pause a non-started operation")
}

// Size returns the src size of the operation
func (o *Operation) Size() int64 {
	o.srcLock()
	var size int64 = 0
	for _, v := range o.src {
		size += v.File.Size()
	}
	max := len(o.src)
	o.srcUnlock()

	o.lock()
	o.logger.Infof("Src Length: %d, Size: %d\n", max, size)
	o.unlock()

	return size
}

// Resume resumes the operation.
func (o *Operation) Resume() error {
	defer o.unlock()
	o.lock()

	if o.status == Paused {
		o.status = Started
		o.logger.Infoln("Resumed the operation")
		return nil
	}

	return fmt.Errorf("cannot resume a non-paused operation")
}

// Proceed is used whenever an error is called. When an operation error
// occurs, the operation gets stuck unless Proceed is called.
func (o *Operation) Proceed() {
	o.lock()
	defer o.unlock()

	o.logger.Infoln("Proceeded with the error")
	o.errWg.Done()
}

// Error returns the error if there is any, or hangs if there isn't an
// error.
func (o *Operation) Error() OperationError {
	o.infoln("Error(): waiting for channel recv")
	err := <-o.err
	o.infoln("Error(): done channel recv")
	return err
}

// Sources returns the list of files that are to be copied ot the destination.
func (o *Operation) Sources() Collection {
	o.srcLock()
	defer o.srcUnlock()
	return o.src
}

// Index returns the index of the current file
func (o *Operation) Index() int {
	o.srcLock()
	defer o.srcUnlock()
	o.infof("Index: %d\n", len(o.src))
	return o.srcIndex
}

// SetSources sets the sources for the operation.
func (o *Operation) SetSources(c Collection) {
	o.srcLock()
	old := len(o.src)
	o.src = c
	o.infof("SetSources(old, new): %d, %d\n", old, len(o.src))
	o.srcUnlock()
}

// do is the main loop for operation, it handles all file transfers
// starting from index. do also adapts to any new files in the collection.
func (o *Operation) do() {
	o.infoln("do()")

	o.once.Do(func() {
		for i := 0; i < len(o.src); i++ {
			select {
			case <-o.exit:
				o.infoln("do(): <-o.exit")
				o.lock()
				if o.status != Aborted && o.status != Finished {
					o.logger.Infoln("Status != Aborted|Finished")
					o.status = Aborted

					close(o.err)
					close(o.exit)
				}
				o.unlock()

				return
			default:
				o.lock()
				if o.status == Paused {
					o.logger.Infoln("do(): paused")
					i--
					o.unlock()
					time.Sleep(time.Millisecond * 100)
					continue
				}
				o.unlock()

				o.srcLock()
				srcFile := o.src[i]
				o.infof("do(): srcFile: %d, %s\n", i, srcFile.File.Name())
				o.srcIndex = i
				o.srcUnlock()

				o.infoln("do(): waiting")
				o.errWg.Wait()
				o.infoln("do(): done waiting")
				o.errWg.Add(1)

				errObj := OperationError{Src: srcFile, Dst: o.dst}
				errOut := func(err error) {
					o.errorf("do(): errOut: %s\n", err)
					errObj.Error = err
					o.err <- errObj
					i--
					o.errWg.Add(1)
				}

				base := path.Base(srcFile.Path)
				err := o.dst.MkdirAll(base, 0755)
				if err != nil {
					errOut(fmt.Errorf("%w: %s", ErrMkdir, err.Error()))
					continue
				}

				_, err = o.dst.Stat(srcFile.Path)
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

				_, err = io.Copy(progressWriter(func(p []byte) (int, error) {
					n, err := dstWriter.Write(p)

					o.lock()
					if o.opProgress != nil {
						o.opProgress.Set(i, int64(n))
					}
					o.unlock()

					return n, err
				}), readerFunc(func(p []byte) (int, error) {
					select {
					case <-o.exit:
						return 0, ErrCancelled
					default:
						for {
							o.lock()
							if o.status != Paused {
								o.unlock()
								break
							}
							o.unlock()
						}
						return srcReader.Read(p)
					}
				}))
				if err != nil {
					errOut(fmt.Errorf("io.Copy: %w", err))
					continue
				}

				dstWriter.Close()
				srcReader.Close()

				o.infof("do(): done transfer: %d, %d, %s\n", i, len(o.src), srcFile.File.Name())
				o.errWg.Done()
				o.infoln("do(): sending error")
				o.err <- errObj
				o.infoln("do(): done")
			}
		}

		o.infoln("do(): loop done")
		o.lock()
		o.status = Finished
		o.unlock()
		o.infoln("do(): closing o.err")
		close(o.err)
		o.infoln("do(): closing o.exit")
		close(o.exit)
	})
}

type PublicOperation struct {
	*Operation
	Destination string `json:"dst"`
}

func (po PublicOperation) Map() map[string]interface{} {
	return map[string]interface{}{
		"index":  po.Index(),
		"src":    po.src,
		"status": po.status,
		"dst":    po.Destination,
	}
}

func (po PublicOperation) MarshalJSON() ([]byte, error) {
	return json.Marshal(po.Map())
}
