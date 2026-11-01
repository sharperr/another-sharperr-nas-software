package backups

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharperr/asns/backend/internal/database"
)

func TestCreateAndStageVerifiedBackup(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "asns.db")
	store, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureAdmin(context.Background(), "admin", "a-unique-password-longer-than-12"); err != nil {
		t.Fatal(err)
	}
	storageRoot := filepath.Join(root, "users")
	userDirectory := filepath.Join(storageRoot, "1")
	if err := os.MkdirAll(userDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDirectory, "notes.txt"), []byte("NAS data"), 0600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, dbPath, storageRoot, filepath.Join(root, "backups"))
	if err := manager.EnsureDestination(""); err != nil {
		t.Fatal(err)
	}
	archive, err := manager.Create(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	archives, err := manager.List("")
	if err != nil || len(archives) != 1 || archives[0].Name != archive.Name {
		t.Fatalf("backup was not listed: %+v, %v", archives, err)
	}
	if err := store.SetBackupSettings(context.Background(), database.BackupSettings{IntervalHours: 24}); err != nil {
		t.Fatal(err)
	}
	stage, err := manager.StageRestore(archive.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stage.FileCount != 2 || stage.TotalSize == 0 {
		t.Fatalf("unexpected staged restore: %+v", stage)
	}
	stagedUserFile := filepath.Join(root, "backups", ".restore-staging", stage.ID, "users", "1", "notes.txt")
	contents, err := os.ReadFile(stagedUserFile)
	if err != nil || string(contents) != "NAS data" {
		t.Fatalf("staged user file mismatch: %q, %v", contents, err)
	}
}

func TestRejectsBackupDestinationTraversal(t *testing.T) {
	manager := NewManager(nil, "", "", filepath.Join(t.TempDir(), "backups"))
	if err := manager.EnsureDestination("..\\outside"); err == nil {
		t.Fatal("expected Windows-style traversal to be rejected")
	}
	if err := manager.EnsureDestination("../outside"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}
