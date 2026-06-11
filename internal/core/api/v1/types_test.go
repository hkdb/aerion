package v1

import (
	"net/http"
	"testing"
)

// stubCore is a no-op implementation of the full Core interface. Its sole
// purpose is to fail compilation if the interfaces drift out of sync with the
// types they reference. If this file stops compiling after an API change,
// every consumer of Core would break the same way.
type stubCore struct{}

func (stubCore) Mail() Mail                   { return stubMail{} }
func (stubCore) Composer() Composer           { return stubComposer{} }
func (stubCore) Contacts() Contacts           { return stubContacts{} }
func (stubCore) Auth() Auth                   { return stubAuth{} }
func (stubCore) Notifications() Notifications { return stubNotifications{} }
func (stubCore) UI() UI                       { return stubUI{} }
func (stubCore) Storage() Storage             { return stubStorage{} }
func (stubCore) Events() EventBus             { return stubEvents{} }
func (stubCore) Log() Logger                  { return stubLogger{} }
func (stubCore) Extension(id string) (any, bool) {
	_ = id
	return nil, false
}

type stubLogger struct{}

func (stubLogger) Debug(string) {}
func (stubLogger) Info(string)  {}
func (stubLogger) Warn(string)  {}
func (stubLogger) Error(string) {}

type stubMail struct{}

func (stubMail) ListMessages(MessageFilter) ([]Message, error)             { return nil, ErrUnimplemented }
func (stubMail) GetMessage(string, bool) (*Message, error)                  { return nil, ErrUnimplemented }
func (stubMail) ListFolders(string) ([]Folder, error)                       { return nil, ErrUnimplemented }
func (stubMail) GetSpecialFolder(string, FolderKind) (*Folder, error)       { return nil, ErrUnimplemented }
func (stubMail) MoveMessage(string, string) error                           { return ErrUnimplemented }
func (stubMail) Archive(string) error                                       { return ErrUnimplemented }
func (stubMail) Trash(string) error                                         { return ErrUnimplemented }
func (stubMail) SetFlags(string, Flags) error                               { return ErrUnimplemented }
func (stubMail) AppendMessage(string, string, []byte, Flags) error          { return ErrUnimplemented }
func (stubMail) SubscribeToMailEvents([]MailEventType) (<-chan MailEvent, Unsubscribe, error) {
	return nil, func() {}, ErrUnimplemented
}

type stubComposer struct{}

func (stubComposer) OpenComposer(ComposeRequest) error { return ErrUnimplemented }

type stubContacts struct{}

func (stubContacts) SearchContacts(string, int) ([]Contact, error) { return nil, ErrUnimplemented }
func (stubContacts) GetContact(string) (*Contact, error)           { return nil, ErrUnimplemented }
func (stubContacts) ListContacts(ContactFilter) ([]Contact, error) { return nil, ErrUnimplemented }
func (stubContacts) ListAddressbooks(string) ([]Addressbook, error) {
	return nil, ErrUnimplemented
}
func (stubContacts) ListSources() ([]ContactSource, error) {
	return nil, ErrUnimplemented
}
func (stubContacts) LinkAccountSource(string, string, int) (string, error) {
	return "", ErrUnimplemented
}
func (stubContacts) SyncSource(string) error      { return ErrUnimplemented }
func (stubContacts) SyncAllSources() error        { return ErrUnimplemented }
func (stubContacts) SetSourceWritable(string, bool) error { return ErrUnimplemented }
func (stubContacts) CreateContact(ContactCreateInput) (string, error) {
	return "", ErrUnimplemented
}
func (stubContacts) UpdateContact(string, ContactPatch) error { return ErrUnimplemented }
func (stubContacts) DeleteContact(string) error               { return ErrUnimplemented }
func (stubContacts) SubscribeToContactEvents([]ContactEventType) (<-chan ContactEvent, Unsubscribe, error) {
	return nil, func() {}, ErrUnimplemented
}

type stubAuth struct{}

func (stubAuth) HTTPClient(string, []AuthScope) (*http.Client, error) { return nil, ErrUnimplemented }
func (stubAuth) IMAPClient(string, []string) (IMAPClient, error)      { return nil, ErrUnimplemented }
func (stubAuth) SMTPClient(string) (SMTPClient, error)                 { return nil, ErrUnimplemented }
func (stubAuth) StartIncrementalConsent(StartIncrementalConsentRequest) error {
	return ErrUnimplemented
}

type stubNotifications struct{}

func (stubNotifications) Show(NotifyRequest) error { return ErrUnimplemented }

type stubUI struct{}

func (stubUI) RegisterRailTab(RailTabRequest) (Unregister, error)               { return func() {}, ErrUnimplemented }
func (stubUI) RegisterSettingsTab(SettingsTabRequest) (Unregister, error)       { return func() {}, ErrUnimplemented }
func (stubUI) RegisterContextMenuItem(ContextMenuRequest) (Unregister, error)   { return func() {}, ErrUnimplemented }
func (stubUI) RegisterInboxView(InboxViewRequest) (Unregister, error)            { return func() {}, ErrUnimplemented }
func (stubUI) RegisterAccountSetupHook(AccountSetupHookRequest) (Unregister, error) {
	return func() {}, ErrUnimplemented
}
func (stubUI) OpenURL(string) error { return ErrUnimplemented }

type stubStorage struct{}

func (stubStorage) KV(string) KVStore         { return stubKV{} }
func (stubStorage) Secrets(string) Secrets    { return stubSecrets{} }
func (stubStorage) HostSecrets() HostSecrets  { return stubHostSecrets{} }

type stubHostSecrets struct{}

func (stubHostSecrets) Get(string) (string, error) { return "", nil }

type stubKV struct{}

func (stubKV) Get(string) (string, error)        { return "", ErrUnimplemented }
func (stubKV) Set(string, string) error          { return ErrUnimplemented }
func (stubKV) Delete(string) error               { return ErrUnimplemented }
func (stubKV) List(string) ([]string, error)     { return nil, ErrUnimplemented }

type stubSecrets struct{}

func (stubSecrets) Set(string, string) error { return ErrUnimplemented }
func (stubSecrets) Get(string) (string, error) { return "", ErrUnimplemented }
func (stubSecrets) Delete(string) error      { return ErrUnimplemented }
func (stubSecrets) DeleteAll() error         { return ErrUnimplemented }

type stubEvents struct{}

func (stubEvents) Publish(string, any) error                              { return ErrUnimplemented }
func (stubEvents) Subscribe(string, func(any)) (Unsubscribe, error)       { return func() {}, ErrUnimplemented }

// TestCoreInterfaceShape asserts the stubs assigned at compile time satisfy
// every interface in this package. If anyone changes a method signature, the
// stub assignment fails to compile and we catch it before runtime.
func TestCoreInterfaceShape(t *testing.T) {
	var c Core = stubCore{}
	_ = c.Mail()
	_ = c.Composer()
	_ = c.Contacts()
	_ = c.Auth()
	_ = c.Notifications()
	_ = c.UI()
	_ = c.Storage()
	_ = c.Events()
	if _, ok := c.Extension("nonexistent"); ok {
		t.Fatal("stubCore.Extension should return false for any id")
	}
}
