package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sharperr/asns/backend/internal/database"
)

const (
	maxArchiveEntries = 100000
	maxArchiveBytes   = int64(1 << 40)
)

type Manager struct {
	store       *database.Store
	database    string
	userStorage string
	root        string
	mu          sync.Mutex
}

type Archive struct {
	Name      string    `json:"name"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}

type Manifest struct {
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"createdAt"`
	Files     map[string]string `json:"files"`
}

type Stage struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	FileCount int       `json:"fileCount"`
	TotalSize int64     `json:"totalSizeBytes"`
}

type sourceFile struct {
	name   string
	path   string
	size   int64
	digest string
}

func NewManager(store *database.Store, databasePath, userStorageRoot, backupRoot string) *Manager {
	return &Manager{store: store, database: databasePath, userStorage: userStorageRoot, root: backupRoot}
}

func (m *Manager) StartScheduler(ctx context.Context) {
	go func() {
		recoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := m.store.RecoverInterruptedBackup(recoveryCtx, time.Now()); err != nil {
			log.Printf("ASNS interrupted backup recovery failed: %v", err)
		}
		cancel()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			m.runScheduled(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m *Manager) runScheduled(ctx context.Context) {
	claimed, err := m.store.ClaimDueBackup(ctx, time.Now())
	if err != nil {
		log.Printf("ASNS scheduled backup check failed: %v", err)
		return
	}
	if !claimed {
		return
	}
	settings, err := m.store.BackupSettings(ctx)
	if err == nil {
		_, err = m.Create(ctx, settings.Destination)
	}
	status := "succeeded"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
		log.Printf("ASNS scheduled backup failed: %v", err)
	}
	if saveErr := m.store.CompleteBackup(context.Background(), status, message); saveErr != nil {
		log.Printf("ASNS scheduled backup result persistence failed: %v", saveErr)
	}
}

func (m *Manager) EnsureDestination(relative string) error {
	_, err := m.destination(relative, true)
	return err
}

func (m *Manager) Delete(destination, name string) error {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".tar.gz") {
		return errors.New("invalid backup archive name")
	}
	directory, err := m.destination(destination, false)
	if err != nil {
		return err
	}
	archivePath := filepath.Join(directory, name)
	info, err := os.Lstat(archivePath)
	if err != nil {
		return fmt.Errorf("inspect backup before removal: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("selected backup is not a regular file")
	}
	if err := os.Remove(archivePath); err != nil {
		return fmt.Errorf("remove backup archive: %w", err)
	}
	return nil
}

func (m *Manager) List(destination string) ([]Archive, error) {
	directory, err := m.destination(destination, false)
	if errors.Is(err, os.ErrNotExist) {
		return []Archive{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list backup destination: %w", err)
	}
	archives := make([]Archive, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect backup archive: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		archives = append(archives, Archive{Name: entry.Name(), SizeBytes: info.Size(), CreatedAt: info.ModTime().UTC()})
	}
	return archives, nil
}

func (m *Manager) Create(ctx context.Context, destination string) (Archive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	directory, err := m.destination(destination, true)
	if err != nil {
		return Archive{}, err
	}
	work, err := os.MkdirTemp(m.root, ".asns-backup-")
	if err != nil {
		return Archive{}, fmt.Errorf("create backup workspace: %w", err)
	}
	defer os.RemoveAll(work)
	dbSnapshot := filepath.Join(work, "database.sqlite")
	if err := m.store.SnapshotDatabase(ctx, dbSnapshot); err != nil {
		return Archive{}, err
	}
	files, err := m.collectSources(dbSnapshot)
	if err != nil {
		return Archive{}, err
	}
	now := time.Now().UTC()
	manifest := Manifest{Version: 1, CreatedAt: now, Files: make(map[string]string, len(files))}
	for _, file := range files {
		manifest.Files[file.name] = file.digest
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return Archive{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	randomID := make([]byte, 8)
	if _, err := rand.Read(randomID); err != nil {
		return Archive{}, fmt.Errorf("generate backup name: %w", err)
	}
	name := "asns-" + now.Format("20060102T150405Z") + "-" + hex.EncodeToString(randomID) + ".tar.gz"
	partial := filepath.Join(directory, "."+name+".partial")
	output, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Archive{}, fmt.Errorf("create backup archive: %w", err)
	}
	writeErr := writeArchive(output, files, manifestBytes)
	closeErr := output.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(partial)
		if writeErr != nil {
			return Archive{}, writeErr
		}
		return Archive{}, fmt.Errorf("close backup archive: %w", closeErr)
	}
	finalPath := filepath.Join(directory, name)
	if err := os.Rename(partial, finalPath); err != nil {
		_ = os.Remove(partial)
		return Archive{}, fmt.Errorf("publish backup archive: %w", err)
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		return Archive{}, fmt.Errorf("inspect completed backup: %w", err)
	}
	return Archive{Name: name, SizeBytes: info.Size(), CreatedAt: now}, nil
}

func (m *Manager) StageRestore(archiveName string) (Stage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if filepath.Base(archiveName) != archiveName || !strings.HasSuffix(archiveName, ".tar.gz") {
		return Stage{}, errors.New("invalid backup archive name")
	}
	settings, err := m.store.BackupSettings(context.Background())
	if err != nil {
		return Stage{}, err
	}
	directory, err := m.destination(settings.Destination, false)
	if err != nil {
		return Stage{}, err
	}
	archivePath := filepath.Join(directory, archiveName)
	info, err := os.Lstat(archivePath)
	if err != nil {
		return Stage{}, fmt.Errorf("open selected backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Stage{}, errors.New("selected backup is not a regular file")
	}
	stageRoot := filepath.Join(m.root, ".restore-staging")
	if err := os.MkdirAll(stageRoot, 0700); err != nil {
		return Stage{}, fmt.Errorf("create restore staging area: %w", err)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Stage{}, fmt.Errorf("generate restore stage ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	stageDir := filepath.Join(stageRoot, id)
	if err := os.Mkdir(stageDir, 0700); err != nil {
		return Stage{}, fmt.Errorf("create restore stage: %w", err)
	}
	result, err := extractAndVerify(archivePath, stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return Stage{}, err
	}
	if err := database.CheckDatabase(filepath.Join(stageDir, "database.sqlite")); err != nil {
		_ = os.RemoveAll(stageDir)
		return Stage{}, fmt.Errorf("backup database integrity check failed: %w", err)
	}
	totalSize, err := directorySize(stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return Stage{}, fmt.Errorf("measure staged restore: %w", err)
	}
	stage := Stage{ID: id, CreatedAt: result.CreatedAt, FileCount: len(result.Files), TotalSize: totalSize}
	stageBytes, err := json.Marshal(stage)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return Stage{}, fmt.Errorf("encode restore stage metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.json"), stageBytes, 0600); err != nil {
		_ = os.RemoveAll(stageDir)
		return Stage{}, fmt.Errorf("save restore stage metadata: %w", err)
	}
	return stage, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("staged restore contains a non-regular file")
		}
		if info.Size() > maxArchiveBytes-total {
			return errors.New("staged restore exceeds maximum size")
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (m *Manager) ApplyStage(id string) error {
	if !validStageID(id) {
		return errors.New("invalid restore stage ID")
	}
	stageDir := filepath.Join(m.root, ".restore-staging", id)
	if err := rejectSymlinks(m.root, filepath.ToSlash(filepath.Join(".restore-staging", id)), false); err != nil {
		return fmt.Errorf("inspect restore stage: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(stageDir, "stage.json"))
	if err != nil {
		return fmt.Errorf("restore stage is unavailable: %w", err)
	}
	var stage Stage
	if err := json.Unmarshal(data, &stage); err != nil || stage.ID != id {
		return errors.New("restore stage metadata is invalid")
	}
	return nil
}

func validStageID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, character := range id {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (m *Manager) collectSources(dbSnapshot string) ([]sourceFile, error) {
	files := make([]sourceFile, 0)
	addFile := func(name, filePath string) error {
		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("inspect backup source: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("backup source %s is not a regular file", name)
		}
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("open backup source: %w", err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash backup source: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close backup source: %w", closeErr)
		}
		files = append(files, sourceFile{name: name, path: filePath, size: info.Size(), digest: hex.EncodeToString(hash.Sum(nil))})
		return nil
	}
	if err := addFile("database.sqlite", dbSnapshot); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(m.userStorage, func(filePath string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && filePath == m.userStorage {
			return nil
		}
		if walkErr != nil {
			return fmt.Errorf("walk ASNS user storage: %w", walkErr)
		}
		if filePath == m.userStorage {
			return nil
		}
		relative, err := filepath.Rel(m.userStorage, filePath)
		if err != nil {
			return fmt.Errorf("resolve user storage source: %w", err)
		}
		archiveName := path.Join("users", filepath.ToSlash(relative))
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if err := addFile(archiveName, filePath); err != nil {
			return err
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return files, nil
}

func writeArchive(output io.Writer, files []sourceFile, manifest []byte) error {
	compressed := gzip.NewWriter(output)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "users/", Mode: 0700, Typeflag: tar.TypeDir, ModTime: time.Now().UTC()}); err != nil {
		_ = archive.Close()
		_ = compressed.Close()
		return fmt.Errorf("write user storage archive header: %w", err)
	}
	for _, source := range files {
		header := &tar.Header{Name: source.name, Mode: 0600, Size: source.size, ModTime: time.Now().UTC(), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			_ = archive.Close()
			_ = compressed.Close()
			return fmt.Errorf("write backup archive header: %w", err)
		}
		file, err := os.Open(source.path)
		if err != nil {
			_ = archive.Close()
			_ = compressed.Close()
			return fmt.Errorf("open source during backup: %w", err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(archive, hash), file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			_ = archive.Close()
			_ = compressed.Close()
			if copyErr != nil {
				return fmt.Errorf("write backup source: %w", copyErr)
			}
			return fmt.Errorf("close backup source: %w", closeErr)
		}
		if hex.EncodeToString(hash.Sum(nil)) != source.digest {
			_ = archive.Close()
			_ = compressed.Close()
			return fmt.Errorf("backup source %s changed while creating archive", source.name)
		}
	}
	header := &tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(manifest)), ModTime: time.Now().UTC(), Typeflag: tar.TypeReg}
	if err := archive.WriteHeader(header); err != nil {
		return fmt.Errorf("write backup manifest header: %w", err)
	}
	if _, err := archive.Write(manifest); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return fmt.Errorf("compress backup archive: %w", err)
	}
	return nil
}

func extractAndVerify(archivePath, stageDir string) (Manifest, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup compression: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	extracted := make(map[string]string)
	var manifest Manifest
	manifestRead := false
	var total int64
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read backup entry: %w", err)
		}
		entries++
		if entries > maxArchiveEntries {
			return Manifest{}, errors.New("backup contains too many entries")
		}
		name := path.Clean(header.Name)
		if name == "." || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") ||
			strings.Contains(header.Name, "\\") || strings.ContainsRune(header.Name, 0) {
			return Manifest{}, errors.New("backup contains an unsafe path")
		}
		if header.Typeflag == tar.TypeDir {
			if name == "manifest.json" || name == "database.sqlite" || name != "users" && !strings.HasPrefix(name, "users/") {
				return Manifest{}, errors.New("backup contains an unexpected directory")
			}
			if err := os.MkdirAll(filepath.Join(stageDir, filepath.FromSlash(name)), 0700); err != nil {
				return Manifest{}, fmt.Errorf("create staged directory: %w", err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 {
			return Manifest{}, errors.New("backup contains an unsupported file type")
		}
		if name == "manifest.json" {
			if manifestRead || header.Size > 1<<20 {
				return Manifest{}, errors.New("backup manifest is invalid")
			}
			data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if err != nil || int64(len(data)) != header.Size {
				return Manifest{}, errors.New("backup manifest is truncated")
			}
			if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != 1 {
				return Manifest{}, errors.New("backup manifest cannot be parsed")
			}
			manifestRead = true
			continue
		}
		if name != "database.sqlite" && !strings.HasPrefix(name, "users/") {
			return Manifest{}, errors.New("backup contains an unexpected file")
		}
		if _, exists := extracted[name]; exists {
			return Manifest{}, errors.New("backup contains duplicate paths")
		}
		if total > maxArchiveBytes-header.Size {
			return Manifest{}, errors.New("backup exceeds the maximum restore size")
		}
		total += header.Size
		target := filepath.Join(stageDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return Manifest{}, fmt.Errorf("create staged file directory: %w", err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return Manifest{}, fmt.Errorf("create staged file: %w", err)
		}
		hash := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(out, hash), reader, header.Size)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			_ = os.Remove(target)
			if copyErr != nil {
				return Manifest{}, fmt.Errorf("extract backup file: %w", copyErr)
			}
			if closeErr != nil {
				return Manifest{}, fmt.Errorf("close staged file: %w", closeErr)
			}
			return Manifest{}, errors.New("backup entry was truncated")
		}
		extracted[name] = hex.EncodeToString(hash.Sum(nil))
	}
	if !manifestRead || manifest.Files == nil || manifest.Files["database.sqlite"] == "" {
		return Manifest{}, errors.New("backup manifest is missing or incomplete")
	}
	if len(manifest.Files) != len(extracted) {
		return Manifest{}, errors.New("backup manifest does not match archive contents")
	}
	for name, digest := range extracted {
		if manifest.Files[name] != digest {
			return Manifest{}, fmt.Errorf("backup integrity check failed for %s", name)
		}
	}
	for name := range manifest.Files {
		if _, exists := extracted[name]; !exists {
			return Manifest{}, errors.New("backup manifest references a missing file")
		}
	}
	if err := os.Remove(filepath.Join(stageDir, "stage.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("remove stale staging metadata: %w", err)
	}
	return manifest, nil
}

func (m *Manager) destination(relative string, create bool) (string, error) {
	if strings.ContainsRune(relative, 0) || strings.Contains(relative, "\\") || filepath.IsAbs(relative) {
		return "", errors.New("backup destination must be relative to the ASNS backup root")
	}
	cleaned := path.Clean(filepath.ToSlash(relative))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("backup destination escapes the ASNS backup root")
	}
	if cleaned == "." {
		cleaned = ""
	}
	if create {
		if err := os.MkdirAll(m.root, 0700); err != nil {
			return "", fmt.Errorf("create ASNS backup root: %w", err)
		}
	}
	if err := rejectSymlinks(m.root, cleaned, create); err != nil {
		return "", err
	}
	result := m.root
	if cleaned != "" {
		result = filepath.Join(m.root, filepath.FromSlash(cleaned))
	}
	return result, nil
}

func rejectSymlinks(root, relative string, create bool) error {
	current := root
	if _, err := os.Lstat(current); err != nil {
		if !create || !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect ASNS backup root: %w", err)
		}
		if err := os.MkdirAll(current, 0700); err != nil {
			return fmt.Errorf("create ASNS backup root: %w", err)
		}
	}
	info, err := os.Lstat(current)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("ASNS backup root must be a real directory")
	}
	if relative == "" {
		return nil
	}
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(current, 0700); err != nil {
				return fmt.Errorf("create backup destination: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect backup destination: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("backup destination must not contain symlinks or files")
		}
	}
	return nil
}
