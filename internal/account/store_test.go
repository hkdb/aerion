package account

import (
	"errors"
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

func validAccountConfig() *AccountConfig {
	return &AccountConfig{
		Name:         "Test Account",
		DisplayName:  "Test User",
		Email:        "test@example.com",
		IMAPHost:     "imap.example.com",
		IMAPPort:     993,
		IMAPSecurity: SecurityTLS,
		SMTPHost:     "smtp.example.com",
		SMTPPort:     587,
		SMTPSecurity: SecurityStartTLS,
		AuthType:     AuthPassword,
		Username:     "test@example.com",
	}
}

func TestAccountConfigValidate(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(c *AccountConfig)
		wantErr   error
		checkFunc func(t *testing.T, c *AccountConfig)
	}{
		{
			name:   "valid config",
			modify: func(c *AccountConfig) {},
		},
		{
			name:    "missing name",
			modify:  func(c *AccountConfig) { c.Name = "" },
			wantErr: ErrNameRequired,
		},
		{
			name:    "missing email",
			modify:  func(c *AccountConfig) { c.Email = "" },
			wantErr: ErrEmailRequired,
		},
		{
			name:    "missing display name",
			modify:  func(c *AccountConfig) { c.DisplayName = "" },
			wantErr: ErrDisplayNameRequired,
		},
		{
			name:    "missing IMAP host",
			modify:  func(c *AccountConfig) { c.IMAPHost = "" },
			wantErr: ErrIMAPHostRequired,
		},
		{
			name:    "missing username",
			modify:  func(c *AccountConfig) { c.Username = "" },
			wantErr: ErrUsernameRequired,
		},
		{
			name: "defaults applied for ports",
			modify: func(c *AccountConfig) {
				c.IMAPPort = 0
				c.SMTPPort = 0
			},
			checkFunc: func(t *testing.T, c *AccountConfig) {
				if c.IMAPPort != 993 {
					t.Errorf("IMAPPort = %d, want 993", c.IMAPPort)
				}
				if c.SMTPPort != 587 {
					t.Errorf("SMTPPort = %d, want 587", c.SMTPPort)
				}
			},
		},
		{
			name: "defaults applied for sync settings",
			modify: func(c *AccountConfig) {
				c.SyncPeriodDays = -1
				c.SyncInterval = -1
			},
			checkFunc: func(t *testing.T, c *AccountConfig) {
				if c.SyncPeriodDays != 30 {
					t.Errorf("SyncPeriodDays = %d, want 30", c.SyncPeriodDays)
				}
				if c.SyncInterval != 30 {
					t.Errorf("SyncInterval = %d, want 30", c.SyncInterval)
				}
			},
		},
		{
			name: "defaults applied for security and auth",
			modify: func(c *AccountConfig) {
				c.IMAPSecurity = ""
				c.SMTPSecurity = ""
				c.AuthType = ""
			},
			checkFunc: func(t *testing.T, c *AccountConfig) {
				if c.IMAPSecurity != SecurityTLS {
					t.Errorf("IMAPSecurity = %q, want %q", c.IMAPSecurity, SecurityTLS)
				}
				if c.SMTPSecurity != SecurityStartTLS {
					t.Errorf("SMTPSecurity = %q, want %q", c.SMTPSecurity, SecurityStartTLS)
				}
				if c.AuthType != AuthPassword {
					t.Errorf("AuthType = %q, want %q", c.AuthType, AuthPassword)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validAccountConfig()
			tt.modify(cfg)
			err := cfg.Validate()

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, cfg)
			}
		})
	}
}

func TestIdentityConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *IdentityConfig
		wantErr error
	}{
		{
			name:   "valid config",
			config: &IdentityConfig{Email: "test@example.com", Name: "Test User"},
		},
		{
			name:    "missing email",
			config:  &IdentityConfig{Email: "", Name: "Test User"},
			wantErr: ErrEmailRequired,
		},
		{
			name:    "missing name",
			config:  &IdentityConfig{Email: "test@example.com", Name: ""},
			wantErr: ErrDisplayNameRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestGetFolderMapping(t *testing.T) {
	acct := &Account{
		SentFolderPath:    "Sent",
		DraftsFolderPath:  "Drafts",
		TrashFolderPath:   "Trash",
		SpamFolderPath:    "Spam",
		ArchiveFolderPath: "Archive",
		AllMailFolderPath: "All Mail",
		StarredFolderPath: "Starred",
	}

	tests := []struct {
		folderType string
		want       string
	}{
		{"sent", "Sent"},
		{"drafts", "Drafts"},
		{"trash", "Trash"},
		{"spam", "Spam"},
		{"archive", "Archive"},
		{"all", "All Mail"},
		{"starred", "Starred"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.folderType, func(t *testing.T) {
			got := acct.GetFolderMapping(tt.folderType)
			if got != tt.want {
				t.Errorf("GetFolderMapping(%q) = %q, want %q", tt.folderType, got, tt.want)
			}
		})
	}
}

func TestStoreCreate(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	cfg := validAccountConfig()
	acct, err := store.Create(cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if acct == nil {
		t.Fatal("Create() returned nil account")
	}
	if acct.ID == "" {
		t.Error("Create() account ID is empty")
	}
	if acct.Name != cfg.Name {
		t.Errorf("Name = %q, want %q", acct.Name, cfg.Name)
	}
	if acct.Email != cfg.Email {
		t.Errorf("Email = %q, want %q", acct.Email, cfg.Email)
	}
	if acct.Color == "" {
		t.Error("Create() auto-assigned color is empty")
	}
	if !acct.Enabled {
		t.Error("Create() account should be enabled by default")
	}
}

func TestStoreGet(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	cfg := validAccountConfig()
	created, err := store.Create(cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Name != created.Name {
		t.Errorf("Name = %q, want %q", got.Name, created.Name)
	}
	if got.Email != created.Email {
		t.Errorf("Email = %q, want %q", got.Email, created.Email)
	}
	if got.IMAPHost != created.IMAPHost {
		t.Errorf("IMAPHost = %q, want %q", got.IMAPHost, created.IMAPHost)
	}
	if got.SMTPHost != created.SMTPHost {
		t.Errorf("SMTPHost = %q, want %q", got.SMTPHost, created.SMTPHost)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestStoreList(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	cfg1 := validAccountConfig()
	cfg2 := validAccountConfig()
	cfg2.Email = "test2@example.com"

	if _, err := store.Create(cfg1); err != nil {
		t.Fatalf("Create(1) error = %v", err)
	}
	if _, err := store.Create(cfg2); err != nil {
		t.Fatalf("Create(2) error = %v", err)
	}

	accounts, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("List() returned %d accounts, want 2", len(accounts))
	}
}

func TestStoreUpdate(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	cfg := validAccountConfig()
	created, err := store.Create(cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cfg.Name = "Updated Account"
	updated, err := store.Update(created.ID, cfg)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Updated Account" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Account")
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get() after Update() error = %v", err)
	}
	if got.Name != "Updated Account" {
		t.Errorf("persisted Name = %q, want %q", got.Name, "Updated Account")
	}
}

func TestStoreDelete(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db)

	cfg := validAccountConfig()
	created, err := store.Create(cfg)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.Delete(created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = store.Get(created.ID)
	if err == nil {
		t.Fatal("Get() after Delete() expected error, got nil")
	}
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Get() after Delete() error = %v, want ErrAccountNotFound", err)
	}
}
