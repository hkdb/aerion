package sync

import "testing"

func TestEmitFolderCounts(t *testing.T) {
	e := &Engine{}

	// No callback: must not panic.
	e.emitFolderCounts("acc", map[string]int{"f1": 3})

	var gotAccount string
	var gotCounts map[string]int
	calls := 0
	e.SetFolderCountsCallback(func(accountID string, counts map[string]int) {
		calls++
		gotAccount = accountID
		gotCounts = counts
	})

	// Empty and nil maps are not delivered.
	e.emitFolderCounts("acc", nil)
	e.emitFolderCounts("acc", map[string]int{})
	if calls != 0 {
		t.Fatalf("empty counts delivered %d times, want 0", calls)
	}

	e.emitFolderCounts("acc", map[string]int{"f1": 3, "f2": 0})
	if calls != 1 || gotAccount != "acc" || gotCounts["f1"] != 3 || gotCounts["f2"] != 0 || len(gotCounts) != 2 {
		t.Fatalf("calls=%d account=%q counts=%v", calls, gotAccount, gotCounts)
	}
}
