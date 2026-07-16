package store

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestNormalizeJournalErrorHidesForeignKeyDetails(t *testing.T) {
	err := normalizeJournalError(&pq.Error{Code: "23503", Constraint: "journal_operation_id_fkey"})
	if !errors.Is(err, ErrInvalidOperationReference) {
		t.Fatalf("error=%v, want ErrInvalidOperationReference", err)
	}
	if err.Error() != ErrInvalidOperationReference.Error() {
		t.Fatalf("foreign-key details leaked through normalized error: %v", err)
	}
}

func TestNormalizeJournalErrorPreservesInternalFailures(t *testing.T) {
	want := errors.New("database unavailable")
	if got := normalizeJournalError(want); !errors.Is(got, want) {
		t.Fatalf("error=%v, want original internal error", got)
	}
}
