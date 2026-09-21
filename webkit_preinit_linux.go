//go:build linux && webkit2_41

package main

/*
#cgo pkg-config: webkit2gtk-4.1
#include <webkit2/webkit2.h>
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// Bind the main goroutine to the main OS thread before any cgo call, so
// preinitWebKit is guaranteed to run on the thread WebKit's assertion checks.
func init() {
	runtime.LockOSThread()
}

// preinitWebKit forces WebKit's one-time WTF initialization to happen now, on
// the main OS thread, before GTK/GLib worker threads exist. WebKitGTK 2.54.0
// added RELEASE_ASSERT(!isMainThread() || Thread::currentSingleton().uid() == 1)
// (WebKit 317619@main), which aborts if the main thread is not the first thread
// its registry ever saw — a race a Go process loses intermittently when the
// first WebKit call happens deep inside Wails window creation (#431). The
// object exists only to trigger the initialization.
func preinitWebKit() {
	mgr := C.webkit_user_content_manager_new()
	C.g_object_unref(C.gpointer(unsafe.Pointer(mgr)))
}
