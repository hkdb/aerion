package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hkdb/aerion/app"
	"github.com/hkdb/aerion/internal/platform"
	"github.com/hkdb/aerion/internal/settings"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed all:frontend/dist
var assets embed.FS

// Command-line flags
var (
	debugMode   = flag.Bool("debug", false, "Enable debug logging")
	composeMode = flag.Bool("compose", false, "Run in composer mode (detached window)")
	accountID   = flag.String("account", "", "Account ID for composer")
	ipcAddress  = flag.String("ipc-address", "", "IPC server address to connect to")
	mode        = flag.String("mode", "new", "Compose mode: new, reply, reply-all, forward")
	messageID   = flag.String("message-id", "", "Original message ID for reply/forward")
	draftID     = flag.String("draft-id", "", "Draft ID to resume editing")
	mailtoFlag  = flag.String("mailto", "", "Mailto URL to open in composer (detached mode)")
	dbusNotify  = flag.Bool("dbus-notify", false, "Use direct D-Bus notifications instead of portal (Linux only)")
)

// DebugMode returns whether debug logging is enabled
// Can be enabled via --debug flag or AERION_DEBUG=1 environment variable
func DebugMode() bool {
	return *debugMode || os.Getenv("AERION_DEBUG") == "1"
}

func main() {
	platform.MonitorGBMErrors()
	flag.Parse()

	// On Windows, GUI apps have no console. Allocate one for debug output.
	if DebugMode() {
		platform.AttachConsole()
	}

	// Check for mailto: URL in non-flag arguments
	var mailtoData *app.MailtoData
	var rawMailtoArg string
	args := flag.Args()
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "mailto:") {
			mailtoData = app.ParseMailtoURL(arg)
			rawMailtoArg = arg
			break
		}
	}

	if *composeMode {
		runComposerMode()
		return
	}
	runMainMode(mailtoData, rawMailtoArg)
}

// runMainMode runs the main application window
func runMainMode(mailtoData *app.MailtoData, rawMailtoArg string) {
	// Determine activation message: pass raw mailto URL if present, otherwise just "show"
	activateMsg := "show"
	if rawMailtoArg != "" {
		activateMsg = rawMailtoArg
	}

	// Single-instance detection: if another instance is running, activate it and exit
	lock := platform.NewSingleInstanceLock()
	locked, err := lock.TryLock(activateMsg)
	if err != nil {
		println("Warning: single-instance check failed:", err.Error())
	}
	if !locked {
		// Existing instance was activated
		return
	}
	defer lock.Unlock()

	// Read native title bar setting before Wails init (Frameless is init-time only)
	nativeTitleBar := false
	if paths, err := platform.GetPaths(); err == nil {
		nativeTitleBar = settings.ReadNativeTitleBar(paths.DatabasePath())
	}

	// Create an instance of the app structure
	application := app.NewApp(DebugMode, *dbusNotify)
	application.SingleInstanceLock = lock

	// Store mailto data if provided (will be used after startup)
	if mailtoData != nil {
		application.PendingMailto = mailtoData
	}

	// Create a dummy ComposerApp for binding generation only.
	// Wails generates JS/TS bindings at build time based on bound structs.
	// We need ComposerApp bindings for the detached composer window.
	dummyComposerApp := app.NewComposerApp(app.ComposerConfig{}, DebugMode)

	// Create application with options
	err = wails.Run(&options.App{
		Title:                    "Aerion",
		Width:                    1280,
		Height:                   800,
		MinWidth:                 360,
		MinHeight:                400,
		Frameless:                !nativeTitleBar,
		StartHidden:              true, // Hide until frontend is ready to prevent white flash
		EnableDefaultContextMenu: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        application.Startup,
		OnDomReady: func(ctx context.Context) {
			platform.NotifyStartupComplete()
		},
		OnShutdown:    application.Shutdown,
		OnBeforeClose: application.BeforeClose,
		Bind: []interface{}{
			application,
			dummyComposerApp, // For binding generation
		},
		Linux: &linux.Options{
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
			ProgramName:      "Aerion",
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

// runComposerMode runs a detached composer window
func runComposerMode() {
	// Validate required flags
	if *accountID == "" {
		println("Error: --account is required for composer mode")
		os.Exit(1)
	}
	if *ipcAddress == "" {
		println("Error: --ipc-address is required for composer mode")
		os.Exit(1)
	}

	// Validate compose mode
	switch *mode {
	case "new", "reply", "reply-all", "forward":
		// valid
	default:
		println("Error: --mode must be one of: new, reply, reply-all, forward")
		os.Exit(1)
	}

	// Create composer configuration
	config := app.ComposerConfig{
		AccountID:  *accountID,
		IPCAddress: *ipcAddress,
		Mode:       *mode,
		MessageID:  *messageID,
		DraftID:    *draftID,
		MailtoURL:  *mailtoFlag,
	}

	// Create composer app
	composerApp := app.NewComposerApp(config, DebugMode)

	// Determine window title based on mode
	title := "New Message"
	switch *mode {
	case "reply":
		title = "Reply"
	case "reply-all":
		title = "Reply All"
	case "forward":
		title = "Forward"
	}
	if *draftID != "" {
		title = "Edit Draft"
	}

	// Read native title bar setting before Wails init (Frameless is init-time only)
	composerNativeTitleBar := false
	if paths, err := platform.GetPaths(); err == nil {
		composerNativeTitleBar = settings.ReadNativeTitleBar(paths.DatabasePath())
	}

	// Create a custom asset handler that serves composer.html instead of index.html
	composerAssetHandler := &composerAssetHandler{assets: assets}

	// Run Wails application for composer window
	err := wails.Run(&options.App{
		Title:                    title,
		Width:                    800,
		Height:                   600,
		MinWidth:                 500,
		MinHeight:                400,
		Frameless:                !composerNativeTitleBar,
		StartHidden:              true, // Hide until frontend is ready to prevent white flash
		EnableDefaultContextMenu: true,
		AssetServer: &assetserver.Options{
			// Don't provide Assets here - we use Handler exclusively
			// so we can rewrite "/" to "/composer.html"
			Handler: composerAssetHandler,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        composerApp.Startup,
		OnShutdown:       composerApp.Shutdown,
		Bind: []interface{}{
			composerApp,
		},
		Linux: &linux.Options{
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
			ProgramName:      "Aerion Composer",
		},
	})

	if err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}

// composerAssetHandler serves composer.html instead of index.html for the root request.
type composerAssetHandler struct {
	assets embed.FS
}

// ServeHTTP implements http.Handler.
// It intercepts requests for "/" and serves composer.html instead.
func (h *composerAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Rewrite root path to composer.html
	if path == "/" || path == "" || path == "/index.html" {
		path = "/composer.html"
	}

	// Try to read from the embedded filesystem
	subFS, err := fs.Sub(h.assets, "frontend/dist")
	if err != nil {
		http.Error(w, "Asset not found", http.StatusNotFound)
		return
	}

	// Create a modified request with the rewritten path
	// This is necessary because http.FileServer uses r.URL.Path
	modifiedReq := new(http.Request)
	*modifiedReq = *r
	modifiedReq.URL = new(url.URL)
	*modifiedReq.URL = *r.URL
	modifiedReq.URL.Path = path

	// Serve the file with the modified request
	http.FileServer(http.FS(subFS)).ServeHTTP(w, modifiedReq)
}
