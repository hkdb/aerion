package sync

import (
	"errors"
	"testing"

	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
)

func TestFolderPathSet(t *testing.T) {
	if got := FolderPathSet(nil); got != nil {
		t.Fatalf("FolderPathSet(nil) = %v, want nil", got)
	}
	set := FolderPathSet([]*folder.Folder{
		{Path: "INBOX"},
		nil,
		{Path: ""},
		{Path: "[Gmail]/Sent Mail"},
	})
	if len(set) != 2 || !set["INBOX"] || !set["[Gmail]/Sent Mail"] {
		t.Fatalf("unexpected set: %v", set)
	}
}

func TestClassifyStatusTargets(t *testing.T) {
	mailboxes := []*imapPkg.Mailbox{
		{Name: "INBOX"},
		{Name: "[Gmail]", Attributes: []string{`\Noselect`, `\HasChildren`}},
		{Name: "[Gmail]/Sent Mail"},
		{Name: "Archive"},
	}
	skip := map[string]bool{"INBOX": true, "[Gmail]/Sent Mail": true}

	results, pending := classifyStatusTargets(mailboxes, skip)

	if len(results) != len(mailboxes) {
		t.Fatalf("results len = %d, want %d", len(results), len(mailboxes))
	}
	for i, r := range results {
		if r.mailbox != mailboxes[i] {
			t.Errorf("result %d mailbox not preserved", i)
		}
	}
	// Skipped: nil status, nil err — falls through the reconcile loop's
	// "STATUS failed" guard and keeps stored counts.
	for _, i := range []int{0, 2} {
		if results[i].status != nil || results[i].err != nil {
			t.Errorf("skipped %q: status=%v err=%v, want nil/nil", mailboxes[i].Name, results[i].status, results[i].err)
		}
	}
	if !errors.Is(results[1].err, errNoSelectMailbox) {
		t.Errorf("\\Noselect container err = %v, want errNoSelectMailbox", results[1].err)
	}
	if len(pending) != 1 || pending[0] != 3 {
		t.Fatalf("pending = %v, want [3]", pending)
	}

	// No skip set: everything selectable is pending.
	_, pending = classifyStatusTargets(mailboxes, nil)
	if len(pending) != 3 {
		t.Fatalf("pending without skip = %v, want 3 entries", pending)
	}
}

func TestChunkIndices(t *testing.T) {
	cases := []struct {
		name string
		n    int
		idx  []int
		want [][]int
	}{
		{"empty", 4, nil, nil},
		{"zero workers", 0, []int{1, 2}, [][]int{{1, 2}}},
		{"fewer than workers", 4, []int{1, 2}, [][]int{{1}, {2}}},
		{"even split", 2, []int{1, 2, 3, 4}, [][]int{{1, 2}, {3, 4}}},
		{"uneven split", 4, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, [][]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, {9}}},
	}
	for _, tc := range cases {
		got := chunkIndices(tc.idx, tc.n)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %d chunks, want %d (%v)", tc.name, len(got), len(tc.want), got)
			continue
		}
		seen := 0
		for i := range got {
			if len(got[i]) != len(tc.want[i]) {
				t.Errorf("%s: chunk %d = %v, want %v", tc.name, i, got[i], tc.want[i])
			}
			for j := range got[i] {
				if got[i][j] != tc.want[i][j] {
					t.Errorf("%s: chunk %d = %v, want %v", tc.name, i, got[i], tc.want[i])
					break
				}
			}
			seen += len(got[i])
		}
		if seen != len(tc.idx) {
			t.Errorf("%s: chunks cover %d indices, want %d", tc.name, seen, len(tc.idx))
		}
	}
}
