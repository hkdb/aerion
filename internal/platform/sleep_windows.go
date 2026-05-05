//go:build windows

package platform

import (
	"context"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/hkdb/aerion/internal/logging"
	"golang.org/x/sys/windows"
)

const (
	wmPowerBroadcast      = 0x0218
	pbtAPMSuspend         = 0x0004
	pbtAPMResumeAutomatic = 0x0012
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

// HWND_MESSAGE = (HWND)-3
const hwndMessage = ^uintptr(2)

// WNDCLASSEXW
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// MSG
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type point struct {
	x, y int32
}

// WindowsSleepWakeMonitor monitors sleep/wake events on Windows
// using a hidden message-only window receiving WM_POWERBROADCAST.
type WindowsSleepWakeMonitor struct {
	events   chan SleepWakeEvent
	stopChan chan struct{}
	wg       sync.WaitGroup
	running  bool
	threadID uint32
	hwnd     uintptr
}

// NewSleepWakeMonitor creates a new sleep/wake monitor for Windows
func NewSleepWakeMonitor() SleepWakeMonitor {
	return &WindowsSleepWakeMonitor{
		events:   make(chan SleepWakeEvent, 10),
		stopChan: make(chan struct{}),
	}
}

// package-level singleton so the WndProc callback can reach the Go instance
var windowsSleepMon *WindowsSleepWakeMonitor

func sleepWndProc(hwnd, umsg, wParam, lParam uintptr) uintptr {
	if umsg == wmPowerBroadcast {
		mon := windowsSleepMon
		if mon != nil {
			var isSleeping bool
			switch wParam {
			case pbtAPMSuspend:
				isSleeping = true
			case pbtAPMResumeAutomatic:
				isSleeping = false
			default:
				ret, _, _ := procDefWindowProcW.Call(hwnd, umsg, wParam, lParam)
				return ret
			}

			event := SleepWakeEvent{
				IsSleeping: isSleeping,
				Timestamp:  time.Now(),
			}

			// Non-blocking send
			select {
			case mon.events <- event:
			default:
			}
		}
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, umsg, wParam, lParam)
	return ret
}

// Start begins monitoring for sleep/wake events
func (m *WindowsSleepWakeMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("sleep-wake")

	if m.running {
		return nil
	}

	windowsSleepMon = m
	m.running = true

	ready := make(chan error, 1)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		m.threadID = windows.GetCurrentThreadId()

		className, _ := windows.UTF16PtrFromString("AerionSleepWake")

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   windows.NewCallback(sleepWndProc),
			lpszClassName: className,
		}

		atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			ready <- err
			return
		}

		hwnd, _, err := procCreateWindowExW.Call(
			0,                                  // dwExStyle
			uintptr(unsafe.Pointer(className)), // lpClassName
			0,                                  // lpWindowName
			0,                                  // dwStyle
			0, 0, 0, 0,                         // x, y, w, h
			hwndMessage, // hWndParent = HWND_MESSAGE
			0,           // hMenu
			0,           // hInstance
			0,           // lpParam
		)
		if hwnd == 0 {
			ready <- err
			return
		}

		m.hwnd = hwnd
		ready <- nil

		// Win32 message loop
		var m2 msg
		for {
			ret, _, _ := procGetMessageW.Call(
				uintptr(unsafe.Pointer(&m2)),
				0, // hWnd = NULL (all messages for this thread)
				0, 0,
			)
			if ret == 0 || ret == ^uintptr(0) {
				// WM_QUIT or error
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m2)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m2)))
		}
	}()

	if err := <-ready; err != nil {
		m.running = false
		windowsSleepMon = nil
		log.Warn().Err(err).Msg("Failed to create sleep/wake monitor window")
		return err
	}

	log.Info().Msg("Sleep/wake monitor started (WM_POWERBROADCAST)")
	return nil
}

// Events returns the channel for receiving sleep/wake events
func (m *WindowsSleepWakeMonitor) Events() <-chan SleepWakeEvent {
	return m.events
}

// Stop stops the monitor and cleans up resources
func (m *WindowsSleepWakeMonitor) Stop() error {
	log := logging.WithComponent("sleep-wake")

	if !m.running {
		return nil
	}

	m.running = false

	// Post WM_QUIT to the message loop thread to unblock GetMessageW
	const wmQuit = 0x0012
	procPostThreadMessageW.Call(uintptr(m.threadID), wmQuit, 0, 0)

	m.wg.Wait()

	if m.hwnd != 0 {
		procDestroyWindow.Call(m.hwnd)
		m.hwnd = 0
	}

	windowsSleepMon = nil

	log.Info().Msg("Sleep/wake monitor stopped (Windows)")
	return nil
}
