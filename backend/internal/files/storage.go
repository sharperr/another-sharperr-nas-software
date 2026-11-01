package files

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Directory bool      `json:"directory"`
	SizeBytes int64     `json:"sizeBytes"`
	Modified  time.Time `json:"modifiedAt"`
}

type Manager struct {
	root string
	mu   sync.Mutex
}

var (
	ErrInvalidPath   = errors.New("invalid storage path")
	ErrQuotaExceeded = errors.New("upload would exceed the user's storage quota")
)

const MaxUploadBytes = 1 << 30

func NewManager(root string) *Manager {
	return &Manager{root: root}
}

func (m *Manager) userRoot(userID int64) (string, error) {
	if userID <= 0 {
		return "", errors.New("invalid storage owner")
	}
	root := filepath.Join(m.root, fmt.Sprintf("%d", userID))
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create user storage: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return "", fmt.Errorf("secure user storage: %w", err)
	}
	return root, nil
}

func cleanRelativePath(value string, allowEmpty bool) (string, error) {
	if strings.ContainsRune(value, 0) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", ErrInvalidPath
	}
	if value == "" && allowEmpty {
		return "", nil
	}
	cleaned := path.Clean(value)
	if cleaned == "." && allowEmpty {
		return "", nil
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return cleaned, nil
}

func resolve(root, relative string, allowEmpty bool) (string, error) {
	cleaned, err := cleanRelativePath(relative, allowEmpty)
	if err != nil {
		return "", err
	}
	target := root
	if cleaned != "" {
		target = filepath.Join(root, filepath.FromSlash(cleaned))
	}
	current := root
	parts := strings.Split(cleaned, "/")
	if cleaned == "" {
		parts = nil
	}
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return "", fmt.Errorf("inspect storage path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalidPath
		}
	}
	return target, nil
}

func (m *Manager) List(userID int64, directory string) ([]Entry, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.userRoot(userID)
	if err != nil {
		return nil, 0, err
	}
	dir, err := resolve(root, directory, true)
	if err != nil {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, fmt.Errorf("read storage directory: %w", err)
	}
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, 0, fmt.Errorf("inspect storage entry: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		relative := entry.Name()
		if directory != "" {
			relative = path.Join(directory, entry.Name())
		}
		size := int64(0)
		if info.Mode().IsRegular() {
			size = info.Size()
		}
		result = append(result, Entry{
			Name: entry.Name(), Path: relative, Directory: info.IsDir(), SizeBytes: size,
			Modified: info.ModTime().UTC(),
		})
	}
	used, err := usage(root)
	if err != nil {
		return nil, 0, err
	}
	return result, used, nil
}

func (m *Manager) Usage(userID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.userRoot(userID)
	if err != nil {
		return 0, err
	}
	return usage(root)
}

func (m *Manager) WithLock(action func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return action()
}

func usage(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk user storage: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect stored file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > int64(^uint64(0)>>1)-total {
			return errors.New("stored data size overflow")
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (m *Manager) Upload(userID int64, relative string, size int64, quota func() (int64, error), source io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size < 0 || size > MaxUploadBytes {
		return errors.New("invalid upload size")
	}
	quotaBytes, err := quota()
	if err != nil {
		return fmt.Errorf("read storage quota: %w", err)
	}
	if quotaBytes < 0 {
		return errors.New("invalid storage quota")
	}
	root, err := m.userRoot(userID)
	if err != nil {
		return err
	}
	destination, err := resolve(root, relative, false)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("open upload directory: %w", err)
	}
	if !parentInfo.IsDir() {
		return errors.New("upload destination is not a directory")
	}
	used, err := usage(root)
	if err != nil {
		return err
	}
	if quotaBytes > 0 && (used > quotaBytes || size > quotaBytes-used) {
		return ErrQuotaExceeded
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create stored file: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != size {
		_ = os.Remove(destination)
		if copyErr != nil {
			return fmt.Errorf("write stored file: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close stored file: %w", closeErr)
		}
		return errors.New("uploaded file size does not match request")
	}
	return nil
}

func (m *Manager) CreateDirectory(userID int64, relative string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.userRoot(userID)
	if err != nil {
		return err
	}
	target, err := resolve(root, relative, false)
	if err != nil {
		return err
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	return nil
}

func (m *Manager) Open(userID int64, relative string) (*os.File, os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.userRoot(userID)
	if err != nil {
		return nil, nil, err
	}
	target, err := resolve(root, relative, false)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect download: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("only regular files can be downloaded")
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, fmt.Errorf("open download: %w", err)
	}
	return file, info, nil
}

func (m *Manager) Remove(userID int64, relative string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	root, err := m.userRoot(userID)
	if err != nil {
		return err
	}
	target, err := resolve(root, relative, false)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove storage entry: %w", err)
	}
	return nil
}
