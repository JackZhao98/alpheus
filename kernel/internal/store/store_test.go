package store

import (
	"errors"
	"net"
	"testing"
	"time"

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

func TestDeadlineConnBoundsBlockedRead(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &deadlineConn{Conn: client, timeout: 25 * time.Millisecond}
	started := time.Now()
	_, err := conn.Read(make([]byte, 1))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("err=%v, want network timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("blocked read took %s", elapsed)
	}
}

func TestNormalizeJournalErrorPreservesInternalFailures(t *testing.T) {
	want := errors.New("database unavailable")
	if got := normalizeJournalError(want); !errors.Is(got, want) {
		t.Fatalf("error=%v, want original internal error", got)
	}
}
