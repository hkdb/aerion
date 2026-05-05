package settings

import (
	"path/filepath"
	"testing"

	"github.com/hkdb/aerion/internal/database"
)

func openTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSetGetReadReceiptPolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    string
		wantErr   bool
		wantValue string
	}{
		{name: "never", policy: "never", wantValue: "never"},
		{name: "ask", policy: "ask", wantValue: "ask"},
		{name: "always", policy: "always", wantValue: "always"},
		{name: "invalid", policy: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetReadReceiptResponsePolicy(tt.policy)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetReadReceiptResponsePolicy()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("got %q, want %q", got, tt.wantValue)
			}
		})
	}
}

func TestSetGetMarkAsReadDelay(t *testing.T) {
	tests := []struct {
		name      string
		delay     int
		wantErr   bool
		wantValue int
	}{
		{name: "immediate", delay: 0, wantValue: 0},
		{name: "manual", delay: -1, wantValue: -1},
		{name: "100ms", delay: 100, wantValue: 100},
		{name: "5000ms", delay: 5000, wantValue: 5000},
		{name: "50ms_invalid", delay: 50, wantErr: true},
		{name: "negative_invalid", delay: -2, wantErr: true},
		{name: "over_max", delay: 5001, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetMarkAsReadDelay(tt.delay)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetMarkAsReadDelay()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("got %d, want %d", got, tt.wantValue)
			}
		})
	}
}

func TestSetGetMessageListDensity(t *testing.T) {
	tests := []struct {
		name    string
		density string
		wantErr bool
	}{
		{name: "micro", density: "micro"},
		{name: "compact", density: "compact"},
		{name: "standard", density: "standard"},
		{name: "large", density: "large"},
		{name: "invalid", density: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetMessageListDensity(tt.density)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetMessageListDensity()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.density {
				t.Errorf("got %q, want %q", got, tt.density)
			}
		})
	}
}

func TestSetGetThemeMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "system", mode: "system"},
		{name: "light", mode: "light"},
		{name: "light-blue", mode: "light-blue"},
		{name: "light-orange", mode: "light-orange"},
		{name: "light-balanced", mode: "light-balanced"},
		{name: "dark", mode: "dark"},
		{name: "dark-gray", mode: "dark-gray"},
		{name: "dark-balanced", mode: "dark-balanced"},
		{name: "dark-catppuccin-mocha", mode: "dark-catppuccin-mocha"},
		{name: "invalid", mode: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetThemeMode(tt.mode)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetThemeMode()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.mode {
				t.Errorf("got %q, want %q", got, tt.mode)
			}
		})
	}
}

func TestSetGetMessageListSortOrder(t *testing.T) {
	tests := []struct {
		name    string
		order   string
		wantErr bool
	}{
		{name: "newest", order: "newest"},
		{name: "oldest", order: "oldest"},
		{name: "invalid", order: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetMessageListSortOrder(tt.order)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetMessageListSortOrder()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.order {
				t.Errorf("got %q, want %q", got, tt.order)
			}
		})
	}
}

func TestSetGetComposerMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "inline", mode: "inline"},
		{name: "detached", mode: "detached"},
		{name: "invalid", mode: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetComposerMode(tt.mode)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetComposerMode()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.mode {
				t.Errorf("got %q, want %q", got, tt.mode)
			}
		})
	}
}

func TestSetGetComposerFormat(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr bool
	}{
		{name: "rich", format: "rich"},
		{name: "plain", format: "plain"},
		{name: "invalid", format: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			err := store.SetComposerFormat(tt.format)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetComposerFormat()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.format {
				t.Errorf("got %q, want %q", got, tt.format)
			}
		})
	}
}

func TestSetGetBoolSettings(t *testing.T) {
	tests := []struct {
		name   string
		set    func(*Store, bool) error
		get    func(*Store) (bool, error)
		value  bool
		defVal bool
	}{
		{
			name:   "ShowTitleBar_true",
			set:    (*Store).SetShowTitleBar,
			get:    (*Store).GetShowTitleBar,
			value:  true,
			defVal: true,
		},
		{
			name:   "ShowTitleBar_false",
			set:    (*Store).SetShowTitleBar,
			get:    (*Store).GetShowTitleBar,
			value:  false,
			defVal: true,
		},
		{
			name:   "TermsAccepted_true",
			set:    (*Store).SetTermsAccepted,
			get:    (*Store).GetTermsAccepted,
			value:  true,
			defVal: false,
		},
		{
			name:   "TermsAccepted_false",
			set:    (*Store).SetTermsAccepted,
			get:    (*Store).GetTermsAccepted,
			value:  false,
			defVal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			// Verify default before setting
			got, err := tt.get(store)
			if err != nil {
				t.Fatalf("unexpected error getting default: %v", err)
			}
			if got != tt.defVal {
				t.Errorf("default: got %v, want %v", got, tt.defVal)
			}

			if err := tt.set(store, tt.value); err != nil {
				t.Fatalf("unexpected error on set: %v", err)
			}

			got, err = tt.get(store)
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.value {
				t.Errorf("got %v, want %v", got, tt.value)
			}
		})
	}
}

func TestSetGetLanguage(t *testing.T) {
	tests := []struct {
		name      string
		language  string
		wantValue string
	}{
		{name: "english", language: "en", wantValue: "en"},
		{name: "empty", language: "", wantValue: ""},
		{name: "zh-TW", language: "zh-TW", wantValue: "zh-TW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(openTestDB(t))

			if err := store.SetLanguage(tt.language); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := store.GetLanguage()
			if err != nil {
				t.Fatalf("unexpected error on get: %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("got %q, want %q", got, tt.wantValue)
			}
		})
	}
}

func TestSetGetRunBackground(t *testing.T) {
	store := NewStore(openTestDB(t))

	// Default is false
	got, err := store.GetRunBackground()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected default false, got true")
	}

	if err := store.SetRunBackground(true); err != nil {
		t.Fatalf("unexpected error on set: %v", err)
	}

	got, err = store.GetRunBackground()
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if !got {
		t.Error("expected true, got false")
	}
}

func TestSetGetAlwaysLoadImages(t *testing.T) {
	store := NewStore(openTestDB(t))

	// Default is false
	got, err := store.GetAlwaysLoadImages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected default false, got true")
	}

	if err := store.SetAlwaysLoadImages(true); err != nil {
		t.Fatalf("unexpected error on set: %v", err)
	}

	got, err = store.GetAlwaysLoadImages()
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if !got {
		t.Error("expected true, got false")
	}
}

func TestGenericSetGet(t *testing.T) {
	store := NewStore(openTestDB(t))

	if err := store.Set("custom_key", "custom_value"); err != nil {
		t.Fatalf("unexpected error on set: %v", err)
	}

	got, err := store.Get("custom_key")
	if err != nil {
		t.Fatalf("unexpected error on get: %v", err)
	}
	if got != "custom_value" {
		t.Errorf("got %q, want %q", got, "custom_value")
	}

	// Overwrite
	if err := store.Set("custom_key", "updated_value"); err != nil {
		t.Fatalf("unexpected error on overwrite: %v", err)
	}

	got, err = store.Get("custom_key")
	if err != nil {
		t.Fatalf("unexpected error on get after overwrite: %v", err)
	}
	if got != "updated_value" {
		t.Errorf("got %q, want %q", got, "updated_value")
	}

	// Get nonexistent key returns empty string
	got, err = store.Get("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
