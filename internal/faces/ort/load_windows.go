//go:build windows

package ort

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows: LoadLibrary + GetProcAddress via x/sys/windows (ORTCHAR_T is
// wchar_t on Windows, so model paths go out as UTF-16).
func dlopen(path string) (uintptr, error) {
	h, err := windows.LoadLibrary(path)
	return uintptr(h), err
}

func dlsym(handle uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(handle), name)
}

func dlclose(handle uintptr) error {
	return windows.FreeLibrary(windows.Handle(handle))
}

func modelPathArg(p string) (uintptr, func()) {
	u16, err := windows.UTF16FromString(p)
	if err != nil {
		u16 = []uint16{0}
	}
	ptr := uintptr(unsafe.Pointer(&u16[0]))
	return ptr, func() { _ = u16[0] }
}
