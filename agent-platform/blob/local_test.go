package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fixedAuthorizer struct {
	authorization ReadAuthorization
	err           error
	calls         int
}

func (authorizer *fixedAuthorizer) AuthorizeBlobRead(_ context.Context, _ ReadRequest) (ReadAuthorization, error) {
	authorizer.calls++
	return authorizer.authorization, authorizer.err
}

type fixedWriteAuthorizer struct{ err error }

func (authorizer fixedWriteAuthorizer) AuthorizeBlobStage(context.Context, StageGrant) error {
	return authorizer.err
}

func (authorizer fixedWriteAuthorizer) AuthorizeBlobMaterialize(context.Context, StagedBlob) error {
	return authorizer.err
}

type fixedDeleteAuthorizer struct{ err error }

func (authorizer fixedDeleteAuthorizer) AuthorizeStageDelete(context.Context, StageDeleteClaim) error {
	return authorizer.err
}

func (authorizer fixedDeleteAuthorizer) AuthorizeContentDelete(context.Context, ContentDeleteClaim) error {
	return authorizer.err
}

func TestLocalStoreStageMaterializeAndAuthorizedRead(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	writes := fixedWriteAuthorizer{}
	content := []byte("hello")
	grant := testGrant(now, "11111111-1111-4111-8111-111111111111", int64(len(content)),
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
	staged, err := store.Stage(context.Background(), grant, bytes.NewReader(content), writes)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Materialize(context.Background(), staged, writes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.stagePath(grant.StageID)); err != nil {
		t.Fatalf("materialize removed retryable stage: %v", err)
	}

	request := ReadRequest{
		PrincipalID: "user-1", BindingID: "attachment-1",
		BlobID:          "22222222-2222-4222-8222-222222222222",
		OwningReference: testRecordRef("user_request", "request-1"),
	}
	ref := BlobRef{
		SchemaRevision: SchemaRevisionV1, BlobID: request.BlobID,
		ContentDigest: staged.ContentDigest, MediaType: grant.MediaType, SizeBytes: staged.SizeBytes,
		Origin: testRecordRef("raw_document", "raw-1"), CommittedAt: now,
	}
	authorizer := &fixedAuthorizer{authorization: ReadAuthorization{
		PrincipalID: request.PrincipalID, BindingID: request.BindingID,
		OwningReference: request.OwningReference, Blob: ref,
		AuthorizedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute),
	}}
	read, err := store.OpenVerified(context.Background(), request, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(read)
	if err != nil || read.Close() != nil || !bytes.Equal(raw, content) {
		t.Fatalf("read raw=%q err=%v", raw, err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorizer calls=%d", authorizer.calls)
	}

	if err := store.DeleteStaged(context.Background(), StageDeleteClaim{
		StageID: grant.StageID, ClaimToken: "gc-stage-1", ValidUntil: now.Add(time.Minute),
	}, fixedDeleteAuthorizer{}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteContent(context.Background(), ContentDeleteClaim{
		ContentDigest: staged.ContentDigest, SizeBytes: staged.SizeBytes,
		ClaimToken: "gc-content-1", ValidUntil: now.Add(time.Minute),
	}, fixedDeleteAuthorizer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenVerified(context.Background(), request, authorizer); !errors.Is(err, ErrBlobMissing) {
		t.Fatalf("missing content read err=%v", err)
	}
}

func TestLocalStoreFailsClosedOnBoundsAuthorizationAndCorruption(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	writes := fixedWriteAuthorizer{}
	grant := testGrant(now, "11111111-1111-4111-8111-111111111111", 5,
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
	if _, err := store.Stage(
		context.Background(), grant, bytes.NewReader([]byte("hello")), fixedWriteAuthorizer{err: errors.New("denied")},
	); !errors.Is(err, ErrBlobUnauthorized) {
		t.Fatalf("unauthorized stage err=%v", err)
	}
	if _, err := store.Stage(context.Background(), grant, bytes.NewReader([]byte("hello!")), writes); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}
	if _, err := os.Stat(store.stagePath(grant.StageID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed stage remained: %v", err)
	}

	badDigest := grant
	badDigest.StageID = "22222222-2222-4222-8222-222222222222"
	badDigest.ExpectedDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.Stage(context.Background(), badDigest, bytes.NewReader([]byte("hello")), writes); !errors.Is(err, ErrBlobDigestMismatch) {
		t.Fatalf("digest mismatch err=%v", err)
	}

	staged, err := store.Stage(context.Background(), grant, bytes.NewReader([]byte("hello")), writes)
	if err != nil || store.Materialize(context.Background(), staged, writes) != nil {
		t.Fatalf("stage/materialize err=%v", err)
	}
	request := ReadRequest{
		PrincipalID: "user-1", BindingID: "attachment-1",
		BlobID:          "33333333-3333-4333-8333-333333333333",
		OwningReference: testRecordRef("user_request", "request-1"),
	}
	denied := &fixedAuthorizer{err: errors.New("denied")}
	if _, err := store.OpenVerified(context.Background(), request, denied); !errors.Is(err, ErrBlobUnauthorized) {
		t.Fatalf("unauthorized err=%v", err)
	}

	path, _ := store.contentPath(staged.ContentDigest)
	if err := store.DeleteContent(context.Background(), ContentDeleteClaim{
		ContentDigest: staged.ContentDigest, SizeBytes: staged.SizeBytes,
		ClaimToken: "gc-content-1", ValidUntil: now.Add(time.Minute),
	}, fixedDeleteAuthorizer{err: errors.New("denied")}); !errors.Is(err, ErrBlobUnauthorized) {
		t.Fatalf("unauthorized content delete err=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unauthorized delete removed content: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("jello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := BlobRef{
		SchemaRevision: SchemaRevisionV1, BlobID: request.BlobID,
		ContentDigest: staged.ContentDigest, MediaType: grant.MediaType, SizeBytes: staged.SizeBytes,
		Origin: testRecordRef("raw_document", "raw-1"), CommittedAt: now,
	}
	allowed := &fixedAuthorizer{authorization: ReadAuthorization{
		PrincipalID: request.PrincipalID, BindingID: request.BindingID,
		OwningReference: request.OwningReference, Blob: ref,
		AuthorizedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute),
	}}
	if _, err := store.OpenVerified(context.Background(), request, allowed); !errors.Is(err, ErrBlobCorrupt) {
		t.Fatalf("corrupt read err=%v", err)
	}
}

func TestLocalStoreConcurrentContentDeduplication(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	writes := fixedWriteAuthorizer{}
	content := []byte("same-content")
	digest := "cae1b3faaa5e4ac7c3306bd164b36dcfdff98294b8024c9c949639b4c480bf6b"
	grants := []StageGrant{
		testGrant(now, "11111111-1111-4111-8111-111111111111", int64(len(content)), digest),
		testGrant(now, "22222222-2222-4222-8222-222222222222", int64(len(content)), digest),
	}
	staged := make([]StagedBlob, len(grants))
	for index := range grants {
		var err error
		staged[index], err = store.Stage(context.Background(), grants[index], bytes.NewReader(content), writes)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errorsByIndex := make([]error, len(staged))
	for index := range staged {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsByIndex[index] = store.Materialize(context.Background(), staged[index], writes)
		}(index)
	}
	wait.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			t.Fatal(err)
		}
	}
	path, _ := store.contentPath(digest)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(content)) {
		t.Fatalf("deduplicated content size=%d", info.Size())
	}
}

func TestLocalStoreRejectsUnsafeRootAndDeleteClaim(t *testing.T) {
	root := t.TempDir()
	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalStore(unsafe); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("unsafe root err=%v", err)
	}
	symlink := filepath.Join(root, "link")
	if err := os.Symlink(root, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalStore(symlink); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("symlink root err=%v", err)
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	if err := store.DeleteContent(context.Background(), ContentDeleteClaim{
		ContentDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:     1, ClaimToken: "gc-1", ValidUntil: now.Add(-time.Second),
	}, fixedDeleteAuthorizer{}); !errors.Is(err, ErrInvalidBlob) {
		t.Fatalf("expired delete claim err=%v", err)
	}
}

func newTestStore(t *testing.T, now time.Time) *LocalStore {
	t.Helper()
	root := filepath.Join(t.TempDir(), "blob")
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	return store
}

func testGrant(now time.Time, stageID string, size int64, digest string) StageGrant {
	return StageGrant{
		SchemaRevision: SchemaRevisionV1, StageID: stageID, PrincipalID: "user-1",
		MediaType: "text/plain; charset=utf-8", MaxBytes: size,
		ExpectedDigest: digest, ExpectedSizeBytes: &size,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
}
