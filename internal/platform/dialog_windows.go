//go:build windows

package platform

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dialogUser32 = windows.NewLazySystemDLL("user32.dll")
	procMsgBoxW  = dialogUser32.NewProc("MessageBoxW")
)

const (
	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconWarning = 0x00000030
	mbIconInfo    = 0x00000040
	mbDefButton2  = 0x00000100
	mbSystemModal = 0x00001000
	mbTopMost     = 0x00040000

	idYes = 6
)

func showDialog(icon DialogIcon, title, text string, block bool) {
	var iconFlag uintptr
	switch icon {
	case DialogIconError:
		iconFlag = mbIconError
	case DialogIconWarning:
		iconFlag = mbIconWarning
	case DialogIconInfo:
		iconFlag = mbIconInfo
	default:
		iconFlag = mbIconInfo
	}
	flags := uintptr(mbOK) | iconFlag | mbSystemModal | mbTopMost

	if block {
		messageBox(title, text, flags)
		return
	}
	go messageBox(title, text, flags)
}

func messageBox(title, text string, flags uintptr) int {
	titlePtr, err1 := windows.UTF16PtrFromString(title)
	textPtr, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", title, text)
		return 0
	}
	ret, _, _ := procMsgBoxW.Call(
		0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		flags,
	)
	return int(ret)
}

// showDialogWithLink renders a Yes/No MessageBox where Yes opens the URL
// and No quits. MessageBoxW doesn't support custom button labels, so the
// body text spells out which button does what.
//
// Future enhancement: switch to TaskDialogIndirect (comctl32) for custom
// button labels and inline hyperlinks. Held back for now to avoid a
// larger syscall surface.
func showDialogWithLink(icon DialogIcon, title, text, actionLabel, actionURL string) {
	var iconFlag uintptr
	switch icon {
	case DialogIconError:
		iconFlag = mbIconError
	case DialogIconWarning:
		iconFlag = mbIconWarning
	case DialogIconInfo:
		iconFlag = mbIconInfo
	default:
		iconFlag = mbIconInfo
	}
	bodyText := fmt.Sprintf(
		"%s\n\n%s\n\nClick \"Yes\" to %s, or \"No\" to quit.",
		text, actionURL, actionLabel,
	)
	// MB_DEFBUTTON2 makes "No" (quit) the default — safer than defaulting to
	// opening a URL when the user just slams Enter.
	flags := uintptr(mbYesNo) | iconFlag | mbDefButton2 | mbSystemModal | mbTopMost
	if messageBox(title, bodyText, flags) == idYes {
		// ShellExecute, not cmd: preserves `&` query params (issue #261).
		_ = OpenURLWindows(actionURL)
	}
}
