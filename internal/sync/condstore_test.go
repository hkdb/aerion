package sync

import "testing"

// TestShouldUseCondStore walks every branch of the truth table the docs
// describe. Each "no" branch tested in isolation so a future refactor that
// drops one of them surfaces immediately.
func TestShouldUseCondStore(t *testing.T) {
	cases := []struct {
		name               string
		uidValidityChanged bool
		prevModSeq         uint64
		mailboxModSeq      uint64
		supportsCondStore  bool
		want               bool
	}{
		{
			name:               "all good: use CONDSTORE",
			uidValidityChanged: false,
			prevModSeq:         100,
			mailboxModSeq:      200,
			supportsCondStore:  true,
			want:               true,
		},
		{
			name:               "UIDValidity changed: must full-sync",
			uidValidityChanged: true,
			prevModSeq:         100,
			mailboxModSeq:      200,
			supportsCondStore:  true,
			want:               false,
		},
		{
			name:               "prevModSeq=0 (first sync ever): must full-sync to capture baseline",
			uidValidityChanged: false,
			prevModSeq:         0,
			mailboxModSeq:      200,
			supportsCondStore:  true,
			want:               false,
		},
		{
			name:               "server didn't return HIGHESTMODSEQ this round: full-sync, don't trust the path",
			uidValidityChanged: false,
			prevModSeq:         100,
			mailboxModSeq:      0,
			supportsCondStore:  true,
			want:               false,
		},
		{
			name:               "server lacks CONDSTORE capability: always full",
			uidValidityChanged: false,
			prevModSeq:         100,
			mailboxModSeq:      200,
			supportsCondStore:  false,
			want:               false,
		},
		{
			name:               "all four no-conditions at once: still false",
			uidValidityChanged: true,
			prevModSeq:         0,
			mailboxModSeq:      0,
			supportsCondStore:  false,
			want:               false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldUseCondStore(tc.uidValidityChanged, tc.prevModSeq, tc.mailboxModSeq, tc.supportsCondStore)
			if got != tc.want {
				t.Errorf("shouldUseCondStore(uidValidityChanged=%v, prevModSeq=%d, mailboxModSeq=%d, supportsCondStore=%v) = %v, want %v",
					tc.uidValidityChanged, tc.prevModSeq, tc.mailboxModSeq, tc.supportsCondStore, got, tc.want)
			}
		})
	}
}

// TestNextModSeq_FlagSyncOK_AdvancesToMailbox: the happy path. Sync succeeded,
// server reported a fresh HIGHESTMODSEQ — persist that, the next cycle will
// CHANGEDSINCE from there.
func TestNextModSeq_FlagSyncOK_AdvancesToMailbox(t *testing.T) {
	got := nextModSeq(true /*flagSyncOK*/, 500 /*mailboxModSeq*/, 100 /*prevModSeq*/)
	if got != 500 {
		t.Errorf("nextModSeq(ok=true, mailbox=500, prev=100) = %d, want 500", got)
	}
}

// TestNextModSeq_FlagSyncFailed_PinsToPrev: THE safety invariant of this PR.
// If a flag sync didn't succeed and we still advance the baseline, the next
// cycle's CHANGEDSINCE filter silently skips whatever the failed cycle
// missed. Forever. nextModSeq exists specifically to prevent that — and the
// test exists specifically to make it impossible to break by mistake.
func TestNextModSeq_FlagSyncFailed_PinsToPrev(t *testing.T) {
	got := nextModSeq(false /*flagSyncOK*/, 500 /*mailboxModSeq*/, 100 /*prevModSeq*/)
	if got != 100 {
		t.Errorf("nextModSeq(ok=false, mailbox=500, prev=100) = %d, want 100 (must pin on failure)", got)
	}
}

// TestNextModSeq_FlagSyncOK_ButMailboxZero: even on a successful flag sync,
// if the server didn't report HIGHESTMODSEQ this round we can't advance —
// advancing to 0 would degenerate the next CONDSTORE check (sinceModSeq=0
// returns the whole mailbox).
func TestNextModSeq_FlagSyncOK_ButMailboxZero(t *testing.T) {
	got := nextModSeq(true /*flagSyncOK*/, 0 /*mailboxModSeq*/, 100 /*prevModSeq*/)
	if got != 100 {
		t.Errorf("nextModSeq(ok=true, mailbox=0, prev=100) = %d, want 100 (must pin when mailbox modseq is 0)", got)
	}
}

// TestNextModSeq_PrevZero_AdvancesOnSuccess: first-ever sync. prev was 0,
// flag sync ran (the full path, since shouldUseCondStore returned false),
// it succeeded, server reported a modseq. We DO want to advance from 0 →
// mailboxModSeq so the next cycle can use the incremental path.
func TestNextModSeq_PrevZero_AdvancesOnSuccess(t *testing.T) {
	got := nextModSeq(true /*flagSyncOK*/, 500 /*mailboxModSeq*/, 0 /*prevModSeq*/)
	if got != 500 {
		t.Errorf("nextModSeq(ok=true, mailbox=500, prev=0) = %d, want 500 (first-sync advancement)", got)
	}
}
