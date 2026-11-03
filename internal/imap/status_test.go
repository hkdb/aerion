package imap

import (
	"context"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestMailboxFromStatus(t *testing.T) {
	if mb := mailboxFromStatus("INBOX", nil); mb == nil || mb.Name != "INBOX" || mb.Messages != 0 {
		t.Fatalf("nil data: got %+v", mb)
	}

	msgs := uint32(120)
	unseen := uint32(7)
	mb := mailboxFromStatus("Archive", &imap.StatusData{
		Mailbox:       "Archive",
		NumMessages:   &msgs,
		NumUnseen:     &unseen,
		UIDNext:       imap.UID(4242),
		UIDValidity:   99,
		HighestModSeq: 1234,
	})
	if mb.Name != "Archive" || mb.Messages != 120 || mb.Unseen != 7 || mb.UIDNext != 4242 || mb.UIDValidity != 99 || mb.HighestModSeq != 1234 {
		t.Fatalf("got %+v", mb)
	}

	// Optional items the server omitted stay zero.
	mb = mailboxFromStatus("Drafts", &imap.StatusData{Mailbox: "Drafts", UIDValidity: 1})
	if mb.Messages != 0 || mb.Unseen != 0 || mb.UIDNext != 0 || mb.HighestModSeq != 0 || mb.UIDValidity != 1 {
		t.Fatalf("omitted items: got %+v", mb)
	}
}

func TestGetMailboxStatuses_NotConnected(t *testing.T) {
	c := NewClient(ClientConfig{})

	statuses, errs := c.GetMailboxStatuses(context.Background(), nil)
	if len(statuses) != 0 || len(errs) != 0 {
		t.Fatalf("empty names: got %d statuses, %d errs", len(statuses), len(errs))
	}

	statuses, errs = c.GetMailboxStatuses(context.Background(), []string{"INBOX", "Sent"})
	if len(statuses) != 2 || len(errs) != 2 {
		t.Fatalf("got %d statuses, %d errs, want 2/2", len(statuses), len(errs))
	}
	for i, err := range errs {
		if err == nil {
			t.Errorf("errs[%d] = nil, want not-connected error", i)
		}
	}

	if _, err := c.GetMailboxStatus(context.Background(), "INBOX"); err == nil {
		t.Fatal("GetMailboxStatus on a disconnected client returned nil error")
	}
}
