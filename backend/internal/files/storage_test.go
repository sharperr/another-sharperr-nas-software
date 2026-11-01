package files

import (
	"bytes"
	"errors"
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
