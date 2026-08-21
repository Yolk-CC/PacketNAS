// Package ort is a minimal, purego-based binding to the ONNX Runtime C API.
//
// It loads libonnxruntime at RUNTIME via dlopen (purego on unix,
// LoadLibrary on windows) so the binary builds with CGO_ENABLED=0 and keeps
// cross-compilation working; when the native library is absent the caller
// degrades gracefully (Open returns an error, no panic).
//
// Only the small subset of the OrtApi needed by PocketNAS faces is wired.
// Struct OrtApi is an append-only ABI: function indices are counted from
// onnxruntime_c_api.h (ORT_API_VERSION 29) and are valid for every runtime
// >= 1.13 (all functions used here predate API 13).
package ort

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ortAPIVersion is requested from OrtApiBase.GetApi. Everything we call
// exists since API 13 (onnxruntime 1.13), so older-but-not-ancient runtime
// libraries keep working; GetApi returns the newest table <= requested.
const ortAPIVersion = 13

// Indices of OrtApi function pointers (0-based), fixed ABI order.
const (
	fnCreateStatus                   = 0
	fnGetErrorCode                   = 1
	fnGetErrorMessage                = 2
	fnCreateEnv                      = 3
	fnCreateSession                  = 7
	fnRun                            = 9
	fnCreateSessionOptions           = 10
	fnSetSessionGraphOptimizationLvl = 23
	fnSetIntraOpNumThreads           = 24
	fnSessionGetInputCount           = 30
	fnSessionGetOutputCount          = 31
	fnSessionGetInputName            = 36
	fnSessionGetOutputName           = 37
	fnCreateTensorWithDataAsOrtValue = 49
	fnGetTensorMutableData           = 51
	fnGetDimensionsCount             = 61
	fnGetDimensions                  = 62
	fnGetTensorTypeAndShape          = 65
	fnCreateMemoryInfo               = 68
	fnAllocatorAlloc                 = 75
	fnAllocatorFree                  = 76
	fnGetAllocatorWithDefaultOptions = 78
	fnReleaseEnv                     = 92
	fnReleaseStatus                  = 93
	fnReleaseMemoryInfo              = 94
	fnReleaseSession                 = 95
	fnReleaseValue                   = 96
	fnReleaseTensorTypeAndShapeInfo  = 99
	fnReleaseSessionOptions          = 100
)

// onnx tensor element types.
const tensorTypeFloat32 = 1

const (
	ortMemTypeDefault    = 0
	ortDeviceAllocator   = 0
	ortLogLevelWarning   = 2
	ortGraphOptEnableAll = 99
)

// Tensor is a float32 tensor with an explicit shape.
type Tensor struct {
	Data  []float32
	Shape []int64
}

// Runtime owns the loaded library handle, the ORT environment and the CPU
// memory info used for all tensor allocations.
type Runtime struct {
	handle  uintptr
	base    uintptr // *OrtApiBase
	api     uintptr // *OrtApi
	env     uintptr // *OrtEnv
	memInfo uintptr // *OrtMemoryInfo
}

var errNotLoaded = errors.New("onnxruntime not loaded")

// apiFn returns the OrtApi function pointer at index idx.
func (r *Runtime) apiFn(idx int) uintptr {
	return *(*uintptr)(unsafe.Pointer(r.api + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

// call invokes OrtApi function idx; if it returns a non-null OrtStatus the
// status is converted to an error (and released).
func (r *Runtime) call(idx int, args ...uintptr) (uintptr, error) {
	r1, _, _ := purego.SyscallN(r.apiFn(idx), args...)
	return r1, r.checkStatus(r1)
}

// callVoid invokes an OrtApi function returning void.
func (r *Runtime) callVoid(idx int, args ...uintptr) {
	purego.SyscallN(r.apiFn(idx), args...)
}

// checkStatus converts an OrtStatus* to an error, releasing it.
func (r *Runtime) checkStatus(status uintptr) error {
	if status == 0 {
		return nil
	}
	msgPtr, _, _ := purego.SyscallN(r.apiFn(fnGetErrorMessage), status)
	msg := cString(msgPtr)
	r.callVoid(fnReleaseStatus, status)
	return fmt.Errorf("onnxruntime: %s", msg)
}

// cString copies a NUL-terminated C string.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var buf []byte
	for i := uintptr(0); ; i++ {
		b := *(*byte)(unsafe.Pointer(p + i))
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

func cBytes(s string) unsafe.Pointer {
	b := append([]byte(s), 0)
	return unsafe.Pointer(&b[0])
}

// cStringBuf returns a NUL-terminated copy of s and its pointer; the caller
// must keep the buffer alive until the C call completes.
func cStringBuf(s string) ([]byte, uintptr) {
	b := append([]byte(s), 0)
	return b, uintptr(unsafe.Pointer(&b[0]))
}

// Open dlopens the onnxruntime shared library at libPath and creates the
// environment. Any failure leaves the process untouched and returns an
// error so the caller can disable face features gracefully.
func Open(libPath string) (*Runtime, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf("onnxruntime binding only supports 64-bit platforms, got %s", runtime.GOARCH)
	}
	handle, err := dlopen(libPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", libPath, err)
	}
	getBase, err := dlsym(handle, "OrtGetApiBase")
	if err != nil {
		dlclose(handle)
		return nil, fmt.Errorf("%s: %w", libPath, err)
	}
	base, _, _ := purego.SyscallN(getBase)
	if base == 0 {
		dlclose(handle)
		return nil, errors.New("OrtGetApiBase returned NULL")
	}
	// OrtApiBase { GetApi(uint32)->*OrtApi, GetVersionString()->*char }
	getApi := *(*uintptr)(unsafe.Pointer(base))
	apiPtr, _, _ := purego.SyscallN(getApi, ortAPIVersion)
	if apiPtr == 0 {
		dlclose(handle)
		return nil, fmt.Errorf("onnxruntime does not provide API version %d", ortAPIVersion)
	}
	r := &Runtime{handle: handle, base: base, api: apiPtr}
	if _, err := r.call(fnCreateEnv,
		uintptr(ortLogLevelWarning),
		uintptr(unsafe.Pointer(cBytes("pocketnas"))),
		uintptr(unsafe.Pointer(&r.env))); err != nil {
		dlclose(handle)
		return nil, err
	}
	if _, err := r.call(fnCreateMemoryInfo,
		uintptr(unsafe.Pointer(cBytes("Cpu"))),
		uintptr(ortDeviceAllocator), 0, uintptr(ortMemTypeDefault),
		uintptr(unsafe.Pointer(&r.memInfo))); err != nil {
		r.callVoid(fnReleaseEnv, r.env)
		dlclose(handle)
		return nil, err
	}
	return r, nil
}

// Version returns the runtime's version string, e.g. "1.17.3".
func (r *Runtime) Version() string {
	if r == nil || r.base == 0 {
		return ""
	}
	getVersionString := *(*uintptr)(unsafe.Pointer(r.base + unsafe.Sizeof(uintptr(0))))
	p, _, _ := purego.SyscallN(getVersionString)
	return cString(p)
}

// Close releases environment and unloads the library.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.memInfo != 0 {
		r.callVoid(fnReleaseMemoryInfo, r.memInfo)
		r.memInfo = 0
	}
	if r.env != 0 {
		r.callVoid(fnReleaseEnv, r.env)
		r.env = 0
	}
	if r.handle != 0 {
		_ = dlclose(r.handle)
		r.handle = 0
	}
}

// Session is one loaded ONNX model with introspected I/O names.
type Session struct {
	rt      *Runtime
	ptr     uintptr // *OrtSession
	Inputs  []string
	Outputs []string
}

// NewSession loads modelPath (full path to a .onnx file).
func (r *Runtime) NewSession(modelPath string, threads int) (*Session, error) {
	if r == nil || r.env == 0 {
		return nil, errNotLoaded
	}
	var opts uintptr
	if _, err := r.call(fnCreateSessionOptions, uintptr(unsafe.Pointer(&opts))); err != nil {
		return nil, err
	}
	defer r.callVoid(fnReleaseSessionOptions, opts)
	if _, err := r.call(fnSetSessionGraphOptimizationLvl, opts, uintptr(ortGraphOptEnableAll)); err != nil {
		return nil, err
	}
	if threads > 0 {
		if _, err := r.call(fnSetIntraOpNumThreads, opts, uintptr(threads)); err != nil {
			return nil, err
		}
	}
	var ptr uintptr
	pathPtr, pathCleanup := modelPathArg(modelPath)
	_, err := r.call(fnCreateSession, r.env, pathPtr, opts, uintptr(unsafe.Pointer(&ptr)))
	pathCleanup()
	if err != nil {
		return nil, fmt.Errorf("load model %s: %w", modelPath, err)
	}
	s := &Session{rt: r, ptr: ptr}
	if err := s.introspect(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// introspect reads input/output tensor names via the default allocator.
func (s *Session) introspect() error {
	var alloc uintptr
	if _, err := s.rt.call(fnGetAllocatorWithDefaultOptions, uintptr(unsafe.Pointer(&alloc))); err != nil {
		return err
	}
	read := func(countFn, nameFn int) ([]string, error) {
		var n uint64
		if _, err := s.rt.call(countFn, s.ptr, uintptr(unsafe.Pointer(&n))); err != nil {
			return nil, err
		}
		out := make([]string, 0, n)
		for i := uint64(0); i < n; i++ {
			var namePtr uintptr
			if _, err := s.rt.call(nameFn, s.ptr, uintptr(i), alloc, uintptr(unsafe.Pointer(&namePtr))); err != nil {
				return nil, err
			}
			out = append(out, cString(namePtr))
			s.rt.callVoid(fnAllocatorFree, alloc, namePtr)
		}
		return out, nil
	}
	var err error
	if s.Inputs, err = read(fnSessionGetInputCount, fnSessionGetInputName); err != nil {
		return err
	}
	if s.Outputs, err = read(fnSessionGetOutputCount, fnSessionGetOutputName); err != nil {
		return err
	}
	return nil
}

// Close releases the session.
func (s *Session) Close() {
	if s != nil && s.ptr != 0 {
		s.rt.callVoid(fnReleaseSession, s.ptr)
		s.ptr = 0
	}
}

// Run feeds inputs (name -> tensor) and returns outputName -> tensor.
// Input data must remain valid for the call duration (kept alive here).
func (s *Session) Run(inputs map[string]Tensor, outputNames []string) (map[string]Tensor, error) {
	if s == nil || s.ptr == 0 {
		return nil, errNotLoaded
	}
	inNames := make([]uintptr, 0, len(inputs))
	inVals := make([]uintptr, 0, len(inputs))
	keepAlive := make([]any, 0, 2*len(inputs)+len(inputs)+len(outputNames))
	for name, t := range inputs {
		flat := int64(1)
		for _, d := range t.Shape {
			flat *= d
		}
		if int64(len(t.Data)) != flat {
			return nil, fmt.Errorf("input %q: %d floats for shape %v", name, len(t.Data), t.Shape)
		}
		var val uintptr
		_, err := s.rt.call(fnCreateTensorWithDataAsOrtValue,
			s.rt.memInfo,
			uintptr(unsafe.Pointer(&t.Data[0])),
			uintptr(len(t.Data)*4),
			uintptr(unsafe.Pointer(&t.Shape[0])),
			uintptr(len(t.Shape)),
			uintptr(tensorTypeFloat32),
			uintptr(unsafe.Pointer(&val)))
		if err != nil {
			return nil, err
		}
		nameBuf, namePtr := cStringBuf(name)
		inNames = append(inNames, namePtr)
		inVals = append(inVals, val)
		keepAlive = append(keepAlive, t.Data, t.Shape, nameBuf)
	}
	defer func() {
		for _, v := range inVals {
			s.rt.callVoid(fnReleaseValue, v)
		}
		runtime.KeepAlive(keepAlive)
	}()

	outNames := make([]uintptr, len(outputNames))
	for i, n := range outputNames {
		buf, ptr := cStringBuf(n)
		outNames[i] = ptr
		keepAlive = append(keepAlive, buf)
	}
	outVals := make([]uintptr, len(outputNames))
	_, err := s.rt.call(fnRun,
		s.ptr, 0,
		uintptr(unsafe.Pointer(&inNames[0])), uintptr(unsafe.Pointer(&inVals[0])), uintptr(len(inNames)),
		uintptr(unsafe.Pointer(&outNames[0])), uintptr(len(outNames)),
		uintptr(unsafe.Pointer(&outVals[0])))
	runtime.KeepAlive(keepAlive)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Tensor, len(outputNames))
	for i, name := range outputNames {
		t, err := s.tensorData(outVals[i])
		s.rt.callVoid(fnReleaseValue, outVals[i])
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", name, err)
		}
		out[name] = t
	}
	return out, nil
}

// tensorData copies shape+data out of an OrtValue (float tensor).
func (s *Session) tensorData(val uintptr) (Tensor, error) {
	var info uintptr
	if _, err := s.rt.call(fnGetTensorTypeAndShape, val, uintptr(unsafe.Pointer(&info))); err != nil {
		return Tensor{}, err
	}
	defer s.rt.callVoid(fnReleaseTensorTypeAndShapeInfo, info)
	var ndim uint64
	if _, err := s.rt.call(fnGetDimensionsCount, info, uintptr(unsafe.Pointer(&ndim))); err != nil {
		return Tensor{}, err
	}
	shape := make([]int64, ndim)
	if ndim > 0 {
		if _, err := s.rt.call(fnGetDimensions, info, uintptr(unsafe.Pointer(&shape[0])), uintptr(ndim)); err != nil {
			return Tensor{}, err
		}
	}
	flat := int64(1)
	for _, d := range shape {
		flat *= d
	}
	var dataPtr uintptr
	if _, err := s.rt.call(fnGetTensorMutableData, val, uintptr(unsafe.Pointer(&dataPtr))); err != nil {
		return Tensor{}, err
	}
	data := make([]float32, flat)
	src := unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), flat)
	copy(data, src)
	return Tensor{Data: data, Shape: shape}, nil
}
