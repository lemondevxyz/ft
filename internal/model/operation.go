package model

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sync"
	"time"

	"github.com/spf13/afero"
)

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

type FileInfo struct {
	Fs   afero.Fs
	File os.FileInfo
	Path string
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
	mtx    sync.Mutex
	status uint8
	once   sync.Once
	err    chan OperationError
	errWg  sync.WaitGroup
	ch     chan struct{}
	srcMtx sync.Mutex
	src    Collection
	dst    afero.Fs
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

type Collection []FileInfo

const (
	Default uint8 = iota
	Started
	Finished
	Aborted
	Paused
)

// NewOperation returns an operation object.
func NewOperation(src Collection, dst afero.Fs) (*Operation, error) {
	return &Operation{
		err: make(chan OperationError),
		ch:  make(chan struct{}),
		src: src,
		dst: dst}, nil
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
	if o.status == Started {
		return fmt.Errorf("already started. either Exit(), Pause(), Resume()")
	}

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
	if o.status == Aborted {
		return fmt.Errorf("already aborted. Start a new instance..")
	}

	if o.status != Default {
		close(o.ch)
	}
	o.status = Aborted

	return nil
}

// Pause pauses the operation temporarily.
func (o *Operation) Pause() error {
	defer o.unlock()
	o.lock()

	if o.status == Started {
		o.status = Paused
		return nil
	}

	return fmt.Errorf("cannot pause a non-started operation")
}

// Resume resumes the operation.
func (o *Operation) Resume() error {
	defer o.unlock()
	o.lock()

	if o.status == Paused {
		o.status = Started
		return nil
	}

	return fmt.Errorf("cannot resume a non-paused operation")
}

// Proceed is used whenever an error is called. When an operation error
// occurs, the operation gets stuck unless Proceed is called.
func (o *Operation) Proceed() {
	o.errWg.Done()
}

// Error returns the error if there is any, or hangs if there isn't an
// error.
func (o *Operation) Error() OperationError {
	return <-o.err
}

// Sources returns the list of files that are to be copied ot the destination.
func (o *Operation) Sources() Collection {
	o.srcMtx.Lock()
	defer o.srcMtx.Unlock()
	return o.src
}

// SetSources sets the sources for the operation.
func (o *Operation) SetSources(c Collection) {
	o.srcMtx.Lock()
	o.src = c
	o.srcMtx.Unlock()
}

func (o *Operation) do() {
	o.once.Do(func() {
		for i := 0; i < len(o.src); i++ {
			select {
			case <-o.ch:
				return
			default:
				o.lock()
				if o.status == Paused {
					o.unlock()
					continue
				}
				o.unlock()

				o.srcMtx.Lock()
				srcFile := o.src[i]
				o.srcMtx.Unlock()
				o.errWg.Wait()

				o.errWg.Add(1)
				errObj := OperationError{Src: srcFile, Dst: o.dst}

				errOut := func(err error) {
					fmt.Println("here", err)
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

				_, err = io.Copy(dstWriter, readerFunc(func(p []byte) (int, error) {
					select {
					case <-o.ch:
						return 0, ErrCancelled
					default:
						return srcReader.Read(p)
					}
				}))
				if err != nil {
					errOut(fmt.Errorf("io.Copy: %s", err.Error()))
					continue
				}

				dstWriter.Close()
				srcReader.Close()

				o.errWg.Done()
				o.err <- errObj
			}
		}

		o.status = Finished
		close(o.err)
		close(o.ch)
	})
}
