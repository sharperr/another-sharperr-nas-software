package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadEnforcesPerUserQuota(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "users"))
	fourByteQuota := func() (int64, error) { return 4, nil }
	if err := manager.Upload(7, "hello.txt", 4, fourByteQuota, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.Upload(7, "too-large.txt", 1, fourByteQuota, bytes.NewReader([]byte("x"))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected quota rejection, got %v", err)
	}
	used, err := manager.Usage(7)
	if err != nil {
		t.Fatal(err)
	}
	if used != 4 {
		t.Fatalf("expected 4 bytes used after rejected upload, got %d", used)
	}
	oneByteQuota := func() (int64, error) { return 1, nil }
	if err := manager.Upload(8, "independent.txt", 1, oneByteQuota, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("another user's quota should be independent: %v", err)
	}
}

func TestManifestIncludesRecursiveHashesWithoutFollowingSymlinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "users")
	manager := NewManager(root)
	userRoot, err := manager.userRoot(12)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(userRoot, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("manifest data")
	if err := os.WriteFile(filepath.Join(userRoot, "folder", "data.txt"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(userRoot, "secret.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	entries, err := manager.Manifest(12, "")
	if err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256(contents)
	foundDirectory, foundFile := false, false
	for _, entry := range entries {
		switch entry.Path {
		case "folder":
			foundDirectory = entry.Directory && entry.SHA256 == ""
		case "folder/data.txt":
			foundFile = !entry.Directory && entry.SizeBytes == int64(len(contents)) &&
				entry.SHA256 == hex.EncodeToString(expectedHash[:])
		case "secret.txt":
			t.Fatal("manifest included a symlink")
		}
	}
	if !foundDirectory || !foundFile {
		t.Fatalf("manifest missing folder or hashed file: %+v", entries)
	}
}

func TestReplaceAtomicallyReplacesFileAndAppliesQuotaToNewSize(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "users"))
	unlimited := func() (int64, error) { return 0, nil }
	if err := manager.Upload(42, "file.txt", 4, unlimited, bytes.NewReader([]byte("old!"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.Replace(42, "file.txt", 6, func() (int64, error) { return 6, nil },
		bytes.NewReader([]byte("newdat"))); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReplaceIfHash(42, "file.txt", "0000000000000000000000000000000000000000000000000000000000000000",
		3, unlimited, bytes.NewReader([]byte("bad"))); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("expected stale replacement to be rejected, got %v", err)
	}
	if err := manager.ReplaceIfHash(42, "new.txt", "", 3, unlimited, bytes.NewReader([]byte("new"))); err != nil {
		t.Fatalf("conditional creation failed: %v", err)
	}
	if err := manager.ReplaceIfHash(42, "new.txt", "", 3, unlimited, bytes.NewReader([]byte("bad"))); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("expected conditional creation to reject an existing file, got %v", err)
	}
	file, _, err := manager.Open(42, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if string(actual) != "newdat" {
		t.Fatalf("replacement bytes = %q", actual)
	}
	newHash := sha256.Sum256([]byte("newdat"))
	if err := manager.RemoveIfHash(42, "file.txt", hex.EncodeToString(newHash[:])); err != nil {
		t.Fatalf("conditional deletion failed: %v", err)
	}
	if err := manager.RemoveIfHash(42, "new.txt", hex.EncodeToString(newHash[:])); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("expected stale deletion to be rejected, got %v", err)
	}
	if err := manager.Replace(42, "file.txt", 7, func() (int64, error) { return 6, nil },
		bytes.NewReader([]byte("toolong"))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected replacement quota rejection, got %v", err)
	}
}

func TestStorageRejectsTraversalAndSymlinks(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "users"))
	unlimited := func() (int64, error) { return 0, nil }
	if err := manager.Upload(3, "../outside.txt", 1, unlimited, bytes.NewReader([]byte("x"))); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
	root, err := manager.userRoot(3)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, _, err := manager.Open(3, "linked.txt"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}
