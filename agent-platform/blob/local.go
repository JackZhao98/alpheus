package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"alpheus/agentplatform/contracts"
)

var (
	ErrBlobTooLarge       = errors.New("blob exceeds granted size")
	ErrBlobDigestMismatch = errors.New("blob digest mismatch")
	ErrBlobSizeMismatch   = errors.New("blob size mismatch")
	ErrBlobUnauthorized   = errors.New("blob read unauthorized")
	ErrBlobMissing        = errors.New("blob bytes missing")
	ErrBlobCorrupt        = errors.New("blob bytes corrupt")
)

type ReadRequest struct {
	PrincipalID     string
	BindingID       string
	BlobID          string
	OwningReference contracts.RecordRef
}

type ReadAuthorization struct {
	PrincipalID     string
	BindingID       string
	OwningReference contracts.RecordRef
	Blob            BlobRef
	AuthorizedAt    time.Time
	ValidUntil      time.Time
}

type ReadAuthorizer interface {
	AuthorizeBlobRead(context.Context, ReadRequest) (ReadAuthorization, error)
}

type StageAuthorizer interface {
	AuthorizeBlobStage(context.Context, StageGrant) error
	AuthorizeBlobMaterialize(context.Context, StagedBlob) error
}

type DeleteAuthorizer interface {
	AuthorizeStageDelete(context.Context, StageDeleteClaim) error
	AuthorizeContentDelete(context.Context, ContentDeleteClaim) error
}

type StageDeleteClaim struct {
	StageID    string
	ClaimToken string
	ValidUntil time.Time
}

type ContentDeleteClaim struct {
	ContentDigest string
	SizeBytes     int64
	ClaimToken    string
	ValidUntil    time.Time
}

type LocalStore struct {
	root string
	now  func() time.Time
}

type VerifiedRead struct {
	file *os.File
	Blob BlobRef
}

func NewLocalStore(root string) (*LocalStore, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrInvalidBlob
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	for _, relative := range []string{"staging", "sha256"} {
		if err := ensurePrivateDirectory(filepath.Join(root, relative)); err != nil {
			return nil, err
		}
	}
	return &LocalStore{root: root, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (store *LocalStore) Stage(ctx context.Context, grant StageGrant, input io.Reader, authorizer StageAuthorizer) (StagedBlob, error) {
	if grant.Validate() != nil || input == nil || authorizer == nil {
		return StagedBlob{}, ErrInvalidBlob
	}
	if err := authorizer.AuthorizeBlobStage(ctx, grant); err != nil {
		return StagedBlob{}, ErrBlobUnauthorized
	}
	now := store.now()
	if now.Before(grant.IssuedAt) || !now.Before(grant.ExpiresAt) {
		return StagedBlob{}, ErrInvalidBlob
	}
	path := store.stagePath(grant.StageID)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return StagedBlob{}, fmt.Errorf("create staged blob: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()

	hash := sha256.New()
	written, err := copyBounded(ctx, io.MultiWriter(file, hash), input, grant.MaxBytes)
	if err != nil {
		return StagedBlob{}, err
	}
	if written < 1 {
		return StagedBlob{}, ErrInvalidBlob
	}
	if err := file.Sync(); err != nil {
		return StagedBlob{}, fmt.Errorf("sync staged blob: %w", err)
	}
	if err := file.Close(); err != nil {
		return StagedBlob{}, fmt.Errorf("close staged blob: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if grant.ExpectedDigest != "" && digest != grant.ExpectedDigest {
		return StagedBlob{}, ErrBlobDigestMismatch
	}
	if grant.ExpectedSizeBytes != nil && written != *grant.ExpectedSizeBytes {
		return StagedBlob{}, ErrBlobSizeMismatch
	}
	stagedAt := store.now()
	result := StagedBlob{
		SchemaRevision: SchemaRevisionV1,
		Grant:          grant, ContentDigest: digest, SizeBytes: written, StagedAt: stagedAt,
	}
	if result.Validate() != nil || !stagedAt.Before(grant.ExpiresAt) {
		return StagedBlob{}, ErrInvalidBlob
	}
	keep = true
	return result, nil
}

// Materialize verifies the staged descriptor and atomically links bytes into
// their content-addressed path. The staged file remains until the caller has
// committed metadata, so a concurrent metadata/GC conflict can be retried.
func (store *LocalStore) Materialize(ctx context.Context, staged StagedBlob, authorizer StageAuthorizer) error {
	if staged.Validate() != nil || authorizer == nil {
		return ErrInvalidBlob
	}
	if err := authorizer.AuthorizeBlobMaterialize(ctx, staged); err != nil {
		return ErrBlobUnauthorized
	}
	stagedPath := store.stagePath(staged.Grant.StageID)
	file, err := openRegularOwnerOnly(stagedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBlobMissing
		}
		return err
	}
	if err := verifyOpenFile(ctx, file, staged.SizeBytes, staged.ContentDigest); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	contentPath, err := store.contentPath(staged.ContentDigest)
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(filepath.Dir(contentPath)); err != nil {
		return err
	}
	if err := os.Link(stagedPath, contentPath); err == nil {
		if err := os.Chmod(contentPath, 0o400); err != nil {
			return fmt.Errorf("protect content blob: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("materialize content blob: %w", err)
	}
	existing, err := openRegularOwnerOnly(contentPath)
	if err != nil {
		return err
	}
	defer existing.Close()
	return verifyOpenFile(ctx, existing, staged.SizeBytes, staged.ContentDigest)
}

func (store *LocalStore) OpenVerified(ctx context.Context, request ReadRequest, authorizer ReadAuthorizer) (*VerifiedRead, error) {
	if authorizer == nil || !validID(request.PrincipalID) || !validID(request.BindingID) ||
		!validUUID(request.BlobID) || request.OwningReference.Validate() != nil {
		return nil, ErrInvalidBlob
	}
	authorization, err := authorizer.AuthorizeBlobRead(ctx, request)
	if err != nil {
		return nil, ErrBlobUnauthorized
	}
	if authorization.Blob.Validate() != nil || authorization.PrincipalID != request.PrincipalID ||
		authorization.BindingID != request.BindingID || authorization.Blob.BlobID != request.BlobID ||
		authorization.OwningReference != request.OwningReference ||
		!orderedUTC(authorization.AuthorizedAt, authorization.ValidUntil) {
		return nil, ErrBlobUnauthorized
	}
	now := store.now()
	if now.Before(authorization.AuthorizedAt) || !now.Before(authorization.ValidUntil) {
		return nil, ErrBlobUnauthorized
	}
	path, err := store.contentPath(authorization.Blob.ContentDigest)
	if err != nil {
		return nil, err
	}
	file, err := openRegularOwnerOnly(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrBlobMissing
		}
		return nil, err
	}
	if err := verifyOpenFile(ctx, file, authorization.Blob.SizeBytes, authorization.Blob.ContentDigest); err != nil {
		_ = file.Close()
		return nil, err
	}
	if !store.now().Before(authorization.ValidUntil) {
		_ = file.Close()
		return nil, ErrBlobUnauthorized
	}
	return &VerifiedRead{file: file, Blob: authorization.Blob}, nil
}

func (read *VerifiedRead) Read(buffer []byte) (int, error) { return read.file.Read(buffer) }

func (read *VerifiedRead) Close() error { return read.file.Close() }

func (store *LocalStore) DeleteStaged(ctx context.Context, claim StageDeleteClaim, authorizer DeleteAuthorizer) error {
	if !validUUID(claim.StageID) || !validID(claim.ClaimToken) || !validUTC(claim.ValidUntil) ||
		!store.now().Before(claim.ValidUntil) || authorizer == nil {
		return ErrInvalidBlob
	}
	if err := authorizer.AuthorizeStageDelete(ctx, claim); err != nil {
		return ErrBlobUnauthorized
	}
	err := os.Remove(store.stagePath(claim.StageID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *LocalStore) DeleteContent(ctx context.Context, claim ContentDeleteClaim, authorizer DeleteAuthorizer) error {
	if !validDigest(claim.ContentDigest) || claim.SizeBytes < 1 || claim.SizeBytes > AbsoluteMaxBytesV1 ||
		!validID(claim.ClaimToken) || !validUTC(claim.ValidUntil) || !store.now().Before(claim.ValidUntil) ||
		authorizer == nil {
		return ErrInvalidBlob
	}
	if err := authorizer.AuthorizeContentDelete(ctx, claim); err != nil {
		return ErrBlobUnauthorized
	}
	path, err := store.contentPath(claim.ContentDigest)
	if err != nil {
		return err
	}
	file, err := openRegularOwnerOnly(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBlobMissing
		}
		return err
	}
	if err := verifyOpenFile(ctx, file, claim.SizeBytes, claim.ContentDigest); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}

func (store *LocalStore) stagePath(stageID string) string {
	return filepath.Join(store.root, "staging", stageID+".blob")
}

func (store *LocalStore) contentPath(digest string) (string, error) {
	if !validDigest(digest) {
		return "", ErrInvalidBlob
	}
	return filepath.Join(store.root, "sha256", digest[:2], digest[2:4], digest+".blob"), nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create blob directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return ErrInvalidBlob
	}
	return nil
}

func openRegularOwnerOnly(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, ErrBlobCorrupt
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrBlobCorrupt
	}
	return file, nil
}

func verifyOpenFile(ctx context.Context, file *os.File, expectedSize int64, expectedDigest string) error {
	info, err := file.Stat()
	if err != nil || info.Size() != expectedSize {
		return ErrBlobSizeMismatch
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := copyBounded(ctx, hash, file, expectedSize); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return ErrBlobCorrupt
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func copyBounded(ctx context.Context, destination io.Writer, source io.Reader, maximum int64) (int64, error) {
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if total+int64(count) > maximum {
				return total, ErrBlobTooLarge
			}
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
		if count == 0 {
			return total, io.ErrNoProgress
		}
	}
}
