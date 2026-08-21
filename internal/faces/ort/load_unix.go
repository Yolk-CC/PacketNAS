//go:build !windows

package ort

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// dlopen loads the shared library (RTLD_NOW|RTLD_LOCAL so the runtime's
// symbols don't leak into the global namespace).
func dlopen(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func dlsym(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}

func dlclose(handle uintptr) error {
	return purego.Dlclose(handle)
}

// modelPathArg passes the model path as a UTF-8 C string on unix.
func modelPathArg(p string) (uintptr, func()) {
	b := append([]byte(p), 0)
	ptr := uintptr(unsafe.Pointer(&b[0]))
	return ptr, func() { _ = b[0] } // keep alive until call returns
}
