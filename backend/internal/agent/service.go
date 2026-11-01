package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sharperr/asns/backend/internal/database"
)

var (
	smbUsernamePattern = regexp.MustCompile(`^asns_[a-z0-9][a-z0-9_-]{0,26}$`)
	shareNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	deviceNamePattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	mdArrayNamePattern = regexp.MustCompile(`^md[0-9A-Za-z_.-]{1,62}$`)
	mdMemberPattern    = regexp.MustCompile(`^([A-Za-z0-9._-]+)\[\d+\](?:\(F\))?$`)
	mdCountPattern     = regexp.MustCompile(`\[(\d+)/(\d+)\]`)
	mdSyncPattern      = regexp.MustCompile(`(?i)\b(resync|recovery|reshape|check|repair)\s*=\s*([0-9]+(?:\.[0-9]+)?)%`)
)

type Permission struct {
	Username string `json:"username"`
	Access   string `json:"access"`
}

type Share struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"createdAt"`
}

type SMBUser struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}

type RestoreJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type restoreStageMetadata struct {
	ID string `json:"id"`
}

type DiskHealth struct {
	Device         string `json:"device"`
	Model          string `json:"model,omitempty"`
	Serial         string `json:"serial,omitempty"`
	Healthy        *bool  `json:"healthy,omitempty"`
	TemperatureC   *int64 `json:"temperatureC,omitempty"`
	SmartAvailable bool   `json:"smartAvailable"`
	Error          string `json:"error,omitempty"`
}

type RAIDArray struct {
	Name          string   `json:"name"`
	Level         string   `json:"level"`
	State         string   `json:"state"`
	Devices       []string `json:"devices"`
	ActiveDevices int      `json:"activeDevices"`
	RaidDevices   int      `json:"raidDevices"`
	Degraded      int      `json:"degraded"`
	SyncAction    string   `json:"syncAction,omitempty"`
	SyncPercent   *float64 `json:"syncPercent,omitempty"`
	MdadmState    string   `json:"mdadmState,omitempty"`
	DetailError   string   `json:"detailError,omitempty"`
}

type persistedState struct {
	Shares []Share   `json:"shares"`
	Users  []SMBUser `json:"users"`
}

type commandRunner interface {
	Run(context.Context, string, []string, string) error
}

type commandOutputRunner interface {
	Output(context.Context, string, []string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args []string, input string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, name, args...)
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (execRunner) Output(ctx context.Context, name string, args []string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Service struct {
	statePath    string
	shareRoot    string
	configPath   string
	fragmentPath string
	deviceRoot   string
	mdstatPath   string
	databasePath string
	userRoot     string
	dataRoot     string
	backupRoot   string
	run          commandRunner
	mu           sync.Mutex
	restoreMu    sync.RWMutex
	restoreJobs  map[string]RestoreJob
}

func NewService(statePath, shareRoot, sambaConfig, sambaFragment string, paths ...string) *Service {
	databasePath := "/var/lib/asns/asns.db"
	userRoot := "/var/lib/asns/users"
	dataRoot := "/var/lib/asns"
	backupRoot := "/var/lib/asns/backups"
	if len(paths) > 0 && paths[0] != "" {
		databasePath = paths[0]
	}
	if len(paths) > 1 && paths[1] != "" {
		userRoot = paths[1]
	}
	if len(paths) > 2 && paths[2] != "" {
		dataRoot = paths[2]
	}
	if len(paths) > 3 && paths[3] != "" {
		backupRoot = paths[3]
	}
	return &Service{
		statePath: statePath, shareRoot: shareRoot, configPath: sambaConfig, fragmentPath: sambaFragment,
		deviceRoot: "/sys/class/block", mdstatPath: "/proc/mdstat", databasePath: databasePath, userRoot: userRoot, dataRoot: dataRoot,
		backupRoot: backupRoot, run: execRunner{}, restoreJobs: make(map[string]RestoreJob),
	}
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/smart", s.smartStatus)
	mux.HandleFunc("GET /v1/raid", s.raidStatus)
	mux.HandleFunc("GET /v1/shares", s.listShares)
	mux.HandleFunc("POST /v1/shares", s.createShare)
	mux.HandleFunc("PUT /v1/shares/{id}", s.updateShare)
	mux.HandleFunc("DELETE /v1/shares/{id}", s.deleteShare)
	mux.HandleFunc("GET /v1/smb-users", s.listSMBUsers)
	mux.HandleFunc("POST /v1/smb-users", s.createSMBUser)
	mux.HandleFunc("PUT /v1/smb-users/{username}/password", s.updateSMBPassword)
	mux.HandleFunc("DELETE /v1/smb-users/{username}", s.deleteSMBUser)
	mux.HandleFunc("POST /v1/restore/apply", s.startRestore)
	mux.HandleFunc("GET /v1/restore/{id}", s.getRestore)
	return mux
}

func (s *Service) Initialize(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	return s.syncSamba(ctx, state)
}

func (s *Service) RemoveSambaIntegration(ctx context.Context) error {
	previousMain, mainExists, err := readOptionalFile(s.configPath)
	if err != nil {
		return err
	}
	previousFragment, fragmentExists, err := readOptionalFile(s.fragmentPath)
	if err != nil {
		return err
	}
	updatedMain, changed := removeSambaInclude(string(previousMain), s.fragmentPath)
	if changed {
		if err := atomicWrite(s.configPath, []byte(updatedMain), 0644); err != nil {
			return fmt.Errorf("remove ASNS Samba include: %w", err)
		}
	}
	if fragmentExists {
		if err := os.Remove(s.fragmentPath); err != nil {
			if changed {
				if restoreErr := restoreFile(s.configPath, previousMain, mainExists); restoreErr != nil {
					return fmt.Errorf("remove ASNS Samba fragment: %v; restore Samba configuration: %w", err, restoreErr)
				}
			}
			return fmt.Errorf("remove ASNS Samba fragment: %w", err)
		}
	}
	if !changed {
		return nil
	}
	if err := s.run.Run(ctx, "testparm", []string{"-s", s.configPath}, ""); err != nil {
		return s.restoreSambaIntegration(previousMain, mainExists, previousFragment, fragmentExists,
			fmt.Errorf("Samba rejected the configuration without ASNS shares: %w", err))
	}
	if err := s.run.Run(ctx, "systemctl", []string{"try-reload-or-restart", "smbd.service"}, ""); err != nil {
		return s.restoreSambaIntegration(previousMain, mainExists, previousFragment, fragmentExists,
			fmt.Errorf("reload Samba after removing ASNS shares: %w", err))
	}
	return nil
}

func (s *Service) restoreSambaIntegration(previousMain []byte, mainExists bool, previousFragment []byte, fragmentExists bool, cause error) error {
	var failures []string
	if err := restoreFile(s.configPath, previousMain, mainExists); err != nil {
		failures = append(failures, "restore Samba configuration: "+err.Error())
	}
	if err := restoreFile(s.fragmentPath, previousFragment, fragmentExists); err != nil {
		failures = append(failures, "restore ASNS Samba fragment: "+err.Error())
	}
	if err := s.run.Run(context.Background(), "systemctl", []string{"try-reload-or-restart", "smbd.service"}, ""); err != nil {
		failures = append(failures, "reload restored Samba configuration: "+err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w; %s", cause, strings.Join(failures, "; "))
	}
	return cause
}

func (s *Service) startRestore(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID string `json:"id"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !validRestoreID(request.ID) {
		writeError(w, http.StatusBadRequest, errors.New("invalid restore stage ID"))
		return
	}
	s.restoreMu.Lock()
	if existing, ok := s.restoreJobs[request.ID]; ok {
		s.restoreMu.Unlock()
		writeJSON(w, http.StatusAccepted, existing)
		return
	}
	job := RestoreJob{ID: request.ID, Status: "queued", UpdatedAt: time.Now().UTC()}
	s.restoreJobs[request.ID] = job
	s.restoreMu.Unlock()
	go s.applyRestore(request.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Service) getRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.restoreMu.RLock()
	job, ok := s.restoreJobs[id]
	s.restoreMu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) applyRestore(id string) {
	s.setRestoreJob(id, "verifying", "")
	stageDir := filepath.Join(s.backupRoot, ".restore-staging", id)
	if err := s.verifyRestoreStage(stageDir, id); err != nil {
		s.setRestoreJob(id, "failed", err.Error())
		return
	}
	s.setRestoreJob(id, "stopping", "")
	if err := s.run.Run(context.Background(), "systemctl", []string{"stop", "asns.service"}, ""); err != nil {
		s.setRestoreJob(id, "failed", fmt.Errorf("stop ASNS before restore: %w", err).Error())
		return
	}
	if err := s.verifyRestoreStage(stageDir, id); err != nil {
		startErr := s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		message := fmt.Errorf("restore stage changed before apply: %w", err).Error()
		if startErr != nil {
			message += "; restart ASNS failed: " + startErr.Error()
		}
		s.setRestoreJob(id, "failed", message)
		return
	}
	if err := ensureNoSymlinkPath(s.dataRoot); err != nil {
		_ = s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		s.setRestoreJob(id, "failed", fmt.Errorf("ASNS data directory is unsafe: %w", err).Error())
		return
	}
	rollbackDir := filepath.Join(s.dataRoot, ".restore-rollback", id)
	if err := os.MkdirAll(rollbackDir, 0700); err != nil {
		_ = s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		s.setRestoreJob(id, "failed", fmt.Errorf("create restore rollback directory: %w", err).Error())
		return
	}
	if err := s.snapshotCurrent(rollbackDir); err != nil {
		_ = s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		s.setRestoreJob(id, "failed", fmt.Errorf("preserve current ASNS data before restore: %w", err).Error())
		return
	}
	if err := s.installRestore(stageDir, rollbackDir); err != nil {
		rollbackErr := s.rollbackRestore(rollbackDir)
		startErr := s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		message := err.Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		if startErr != nil {
			message += "; restart after rollback failed: " + startErr.Error()
		}
		s.setRestoreJob(id, "failed", message)
		return
	}
	s.setRestoreJob(id, "starting", "")
	if err := s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, ""); err != nil {
		rollbackErr := s.rollbackRestore(rollbackDir)
		startErr := s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		message := fmt.Errorf("start ASNS after restore: %w", err).Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		if startErr != nil {
			message += "; restart after rollback failed: " + startErr.Error()
		}
		s.setRestoreJob(id, "failed", message)
		return
	}
	if err := waitForASNS(60 * time.Second); err != nil {
		_ = s.run.Run(context.Background(), "systemctl", []string{"stop", "asns.service"}, "")
		rollbackErr := s.rollbackRestore(rollbackDir)
		startErr := s.run.Run(context.Background(), "systemctl", []string{"start", "asns.service"}, "")
		message := fmt.Errorf("restored ASNS service did not become healthy: %w", err).Error()
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		if startErr != nil {
			message += "; restart after rollback failed: " + startErr.Error()
		}
		s.setRestoreJob(id, "failed", message)
		return
	}
	s.setRestoreJob(id, "succeeded", "")
}

func waitForASNS(timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:8080/api/v1/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	return lastErr
}

func (s *Service) verifyRestoreStage(stageDir, id string) error {
	if !withinRoot(s.backupRoot, stageDir) {
		return errors.New("restore stage is outside the configured backup root")
	}
	if err := ensureNoSymlinkPath(stageDir); err != nil {
		return fmt.Errorf("inspect restore stage path: %w", err)
	}
	info, err := os.Lstat(stageDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("restore stage is missing or unsafe")
	}
	var stage restoreStageMetadata
	data, err := os.ReadFile(filepath.Join(stageDir, "stage.json"))
	if err != nil {
		return fmt.Errorf("read restore stage metadata: %w", err)
	}
	if err := json.Unmarshal(data, &stage); err != nil || stage.ID != id {
		return errors.New("restore stage metadata is invalid")
	}
	dbPath := filepath.Join(stageDir, "database.sqlite")
	databaseInfo, err := os.Lstat(dbPath)
	if err != nil || !databaseInfo.Mode().IsRegular() {
		return errors.New("restored database file is missing or unsafe")
	}
	if err := database.CheckDatabase(dbPath); err != nil {
		return fmt.Errorf("restored SQLite database failed integrity check: %w", err)
	}
	if err := validateTree(filepath.Join(stageDir, "users")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("restore user files are invalid: %w", err)
	}
	return nil
}

func (s *Service) snapshotCurrent(rollbackDir string) error {
	if err := copyRegular(s.databasePath, filepath.Join(rollbackDir, "database.sqlite")); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		source := s.databasePath + suffix
		if _, err := os.Lstat(source); err == nil {
			if err := copyRegular(source, filepath.Join(rollbackDir, "database.sqlite"+suffix)); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect current SQLite sidecar: %w", err)
		}
	}
	if err := copyTree(s.userRoot, filepath.Join(rollbackDir, "users")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Service) installRestore(stageDir, rollbackDir string) error {
	if err := ensureSafeParent(s.dataRoot, s.databasePath); err != nil {
		return err
	}
	temporaryDB := s.databasePath + ".restore-" + filepath.Base(stageDir)
	if err := copyRegular(filepath.Join(stageDir, "database.sqlite"), temporaryDB); err != nil {
		return fmt.Errorf("prepare restored database: %w", err)
	}
	if err := os.Chmod(temporaryDB, 0600); err != nil {
		_ = os.Remove(temporaryDB)
		return fmt.Errorf("secure restored database: %w", err)
	}
	if err := setOwner(temporaryDB, "asns"); err != nil {
		_ = os.Remove(temporaryDB)
		return err
	}
	if err := replaceTree(filepath.Join(stageDir, "users"), s.userRoot); err != nil {
		_ = os.Remove(temporaryDB)
		return fmt.Errorf("install restored user files: %w", err)
	}
	if err := setTreeOwner(s.userRoot, "asns"); err != nil {
		_ = os.Remove(temporaryDB)
		return fmt.Errorf("set restored user storage ownership: %w", err)
	}
	if err := os.Rename(s.databasePath, filepath.Join(rollbackDir, "database.sqlite.current")); err != nil {
		return fmt.Errorf("preserve current database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Rename(s.databasePath+suffix, filepath.Join(rollbackDir, "database.sqlite.current"+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Rename(filepath.Join(rollbackDir, "database.sqlite.current"), s.databasePath)
			return fmt.Errorf("preserve current SQLite sidecar: %w", err)
		}
	}
	if err := os.Rename(temporaryDB, s.databasePath); err != nil {
		_ = os.Rename(filepath.Join(rollbackDir, "database.sqlite.current"), s.databasePath)
		return fmt.Errorf("activate restored database: %w", err)
	}
	return nil
}

func (s *Service) rollbackRestore(rollbackDir string) error {
	var failures []string
	databaseBackup := filepath.Join(rollbackDir, "database.sqlite.current")
	if _, err := os.Stat(databaseBackup); err == nil {
		_ = os.Remove(s.databasePath)
		if err := os.Rename(databaseBackup, s.databasePath); err != nil {
			failures = append(failures, "restore previous database: "+err.Error())
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		backup := filepath.Join(rollbackDir, "database.sqlite.current"+suffix)
		if _, err := os.Stat(backup); err == nil {
			_ = os.Remove(s.databasePath + suffix)
			if err := os.Rename(backup, s.databasePath+suffix); err != nil {
				failures = append(failures, "restore SQLite sidecar: "+err.Error())
			}
		}
	}
	previousUsers := filepath.Join(rollbackDir, "users")
	if _, err := os.Stat(previousUsers); err == nil {
		if err := replaceTree(previousUsers, s.userRoot); err != nil {
			failures = append(failures, "restore previous user files: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) setRestoreJob(id, status, message string) {
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()
	s.restoreJobs[id] = RestoreJob{ID: id, Status: status, Error: message, UpdatedAt: time.Now().UTC()}
}

func validRestoreID(id string) bool {
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

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func ensureSafeParent(root, target string) error {
	if !withinRoot(root, target) {
		return errors.New("restore target is outside ASNS data directory")
	}
	parent := filepath.Dir(target)
	current := root
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect restore target parent: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("restore target parent is unsafe")
		}
	}
	return nil
}

func ensureNoSymlinkPath(target string) error {
	cleaned := filepath.Clean(target)
	volume := filepath.VolumeName(cleaned)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(cleaned, current)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path contains a symlink")
		}
	}
	return nil
}

func setTreeOwner(root, username string) error {
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := setOwner(filePath, username); err != nil {
			return err
		}
		return nil
	})
}

func setOwner(path, username string) error {
	account, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("find service account for restored files: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse service account UID: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parse service account GID: %w", err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("set restored file owner: %w", err)
	}
	return nil
}

func validateTree(root string) error {
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !(info.IsDir() || info.Mode().IsRegular()) {
			return errors.New("tree contains a symlink or special file")
		}
		return nil
	})
}

func copyRegular(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("restore source is not a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(destination)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return nil
}

func copyTree(source, destination string) error {
	if err := validateTree(source); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			return os.Chmod(target, 0700)
		}
		if err := copyRegular(sourcePath, target); err != nil {
			return err
		}
		return os.Chmod(target, 0600)
	})
}

func replaceTree(source, destination string) error {
	temporary := destination + ".restore"
	_ = os.RemoveAll(temporary)
	if err := copyTree(source, temporary); err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	return os.Rename(temporary, destination)
}

func (s *Service) smartStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := exec.LookPath("smartctl"); err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("smartmontools is not installed"))
		return
	}
	entries, err := os.ReadDir(s.deviceRoot)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("discover block devices: %w", err))
		return
	}
	result := make([]DiskHealth, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !deviceNamePattern.MatchString(name) || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "fd") {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.deviceRoot, name, "device")); err != nil {
			continue
		}
		result = append(result, readDiskHealth(r.Context(), name))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Device < result[j].Device })
	writeJSON(w, http.StatusOK, map[string]any{"disks": result})
}

func readDiskHealth(ctx context.Context, name string) DiskHealth {
	result := DiskHealth{Device: name, SmartAvailable: true}
	if !deviceNamePattern.MatchString(name) {
		result.Error = "invalid device name"
		return result
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, "smartctl", "--json", "-H", "-A", "/dev/"+name)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || len(output) == 0 {
			result.Error = "SMART query failed"
			log.Printf("ASNS SMART query failed for %s: %v", name, err)
			return result
		}
	}
	var response smartctlResponse
	if err := json.Unmarshal(output, &response); err != nil {
		result.Error = "SMART response could not be parsed"
		log.Printf("ASNS SMART response parse failed for %s: %v", name, err)
		return result
	}
	result.Model = response.ModelName
	result.Serial = response.SerialNumber
	if response.SmartStatus.Passed != nil {
		healthy := *response.SmartStatus.Passed
		result.Healthy = &healthy
	}
	temperature := response.Temperature.Current
	if temperature == 0 {
		temperature = response.NVMeHealth.Temperature
	}
	if temperature > 0 {
		value := int64(temperature)
		result.TemperatureC = &value
	} else {
		for _, attribute := range response.Attributes.Table {
			if strings.Contains(strings.ToLower(attribute.Name), "temperature") && attribute.Raw.Value > 0 {
				value := attribute.Raw.Value
				result.TemperatureC = &value
				break
			}
		}
	}
	if result.Healthy == nil && result.TemperatureC == nil {
		result.Error = "SMART health and temperature are not reported by this device"
	}
	return result
}

func (s *Service) raidStatus(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.mdstatPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("read Linux RAID status: %w", err))
		return
	}
	arrays, err := parseMDStat(data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	outputRunner, ok := s.run.(commandOutputRunner)
	if !ok && len(arrays) > 0 {
		writeError(w, http.StatusServiceUnavailable, errors.New("mdadm detail command is unavailable"))
		return
	}
	for index := range arrays {
		name := arrays[index].Name
		output, err := outputRunner.Output(r.Context(), "mdadm", []string{"--detail", "--export", "/dev/" + name})
		if err != nil {
			arrays[index].DetailError = "mdadm could not read array details"
			log.Printf("ASNS mdadm detail unavailable for %s: %v", name, err)
			continue
		}
		arrays[index].MdadmState = mdadmState(output)
	}
	writeJSON(w, http.StatusOK, map[string]any{"arrays": arrays})
}

func parseMDStat(data []byte) ([]RAIDArray, error) {
	lines := strings.Split(string(data), "\n")
	arrays := make([]RAIDArray, 0)
	for index := 0; index < len(lines); index++ {
		fields := strings.Fields(lines[index])
		if len(fields) < 4 || fields[1] != ":" {
			continue
		}
		name := fields[0]
		if !mdArrayNamePattern.MatchString(name) {
			continue
		}
		array := RAIDArray{Name: name, State: "active", Devices: []string{}}
		deviceStart := 3
		if fields[2] == "inactive" {
			array.State = "inactive"
			array.Level = "unknown"
			if len(fields) > 3 && strings.HasPrefix(fields[3], "raid") {
				array.Level = fields[3]
				deviceStart = 4
			}
		} else {
			array.Level = fields[3]
			deviceStart = 4
		}
		faultyMember := false
		for _, field := range fields[deviceStart:] {
			match := mdMemberPattern.FindStringSubmatch(field)
			if match == nil {
				continue
			}
			array.Devices = append(array.Devices, match[1])
			if strings.HasSuffix(field, "(F)") {
				faultyMember = true
			}
		}
		for next := index + 1; next < len(lines); next++ {
			nextFields := strings.Fields(lines[next])
			if len(nextFields) > 1 && nextFields[1] == ":" {
				break
			}
			if counts := mdCountPattern.FindStringSubmatch(lines[next]); counts != nil {
				array.RaidDevices, _ = strconv.Atoi(counts[1])
				array.ActiveDevices, _ = strconv.Atoi(counts[2])
			}
			if strings.Contains(lines[next], "[") && strings.Contains(lines[next], "]") {
				indices := strings.Index(lines[next], "[")
				end := strings.Index(lines[next][indices:], "]")
				if end > 0 {
					marks := lines[next][indices+1 : indices+end]
					if strings.Trim(marks, "U_") == "" && len(marks) > 0 {
						if array.RaidDevices == 0 {
							array.RaidDevices = len(marks)
						}
						array.ActiveDevices = strings.Count(marks, "U")
					}
				}
			}
			if sync := mdSyncPattern.FindStringSubmatch(lines[next]); sync != nil {
				array.SyncAction = strings.ToLower(sync[1])
				percent, err := strconv.ParseFloat(sync[2], 64)
				if err == nil {
					array.SyncPercent = &percent
				}
			}
		}
		if array.State != "inactive" {
			if faultyMember || (array.RaidDevices > 0 && array.ActiveDevices < array.RaidDevices) {
				array.State = "degraded"
			} else if array.RaidDevices > 0 {
				array.State = "healthy"
			}
		}
		sort.Strings(array.Devices)
		arrays = append(arrays, array)
	}
	return arrays, nil
}

func mdadmState(output []byte) string {
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && key == "MD_STATE" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type smartctlResponse struct {
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	SmartStatus  struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current int64 `json:"current"`
	} `json:"temperature"`
	NVMeHealth struct {
		Temperature int64 `json:"temperature"`
	} `json:"nvme_smart_health_information_log"`
	Attributes struct {
		Table []struct {
			Name string `json:"name"`
			Raw  struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
}

func (s *Service) listShares(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": state.Shares})
}

func (s *Service) listSMBUsers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": state.Users})
}

func (s *Service) createShare(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string       `json:"name"`
		Permissions []Permission `json:"permissions"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !shareNamePattern.MatchString(request.Name) {
		writeError(w, http.StatusBadRequest, errors.New("share name may contain only letters, numbers, underscores, and hyphens"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, current := range state.Shares {
		if strings.EqualFold(current.Name, request.Name) {
			writeError(w, http.StatusConflict, errors.New("a share with that name already exists"))
			return
		}
	}
	if err := validatePermissions(request.Permissions, state.Users); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("generate share identifier: %w", err))
		return
	}
	shareDirectory := filepath.Join(s.shareRoot, hex.EncodeToString(idBytes))
	if err := ensureDirectory(shareDirectory); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := ensureShareOwner(r.Context(), s.run); err != nil {
		_ = os.Remove(shareDirectory)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.run.Run(r.Context(), "chown", []string{"asns-share:asns-share", shareDirectory}, ""); err != nil {
		_ = os.Remove(shareDirectory)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	share := Share{
		ID: hex.EncodeToString(idBytes), Name: request.Name, Path: shareDirectory,
		Permissions: request.Permissions, CreatedAt: time.Now().UTC(),
	}
	state.Shares = append(state.Shares, share)
	if err := s.saveAndApply(r.Context(), state); err != nil {
		_ = os.Remove(shareDirectory)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, share)
}

func (s *Service) updateShare(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string       `json:"name"`
		Permissions []Permission `json:"permissions"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !shareNamePattern.MatchString(request.Name) {
		writeError(w, http.StatusBadRequest, errors.New("invalid share name"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validatePermissions(request.Permissions, state.Users); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	index := findShare(state.Shares, r.PathValue("id"))
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	for i, current := range state.Shares {
		if i != index && strings.EqualFold(current.Name, request.Name) {
			writeError(w, http.StatusConflict, errors.New("a share with that name already exists"))
			return
		}
	}
	state.Shares[index].Name = request.Name
	state.Shares[index].Permissions = request.Permissions
	if err := s.saveAndApply(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, state.Shares[index])
}

func (s *Service) deleteShare(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	index := findShare(state.Shares, r.PathValue("id"))
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	state.Shares = append(state.Shares[:index], state.Shares[index+1:]...)
	if err := s.saveAndApply(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) createSMBUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !smbUsernamePattern.MatchString(request.Username) || !validPassword(request.Password) {
		writeError(w, http.StatusBadRequest, errors.New("SMB usernames must start with asns_ and passwords must contain 12-72 characters"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, user := range state.Users {
		if user.Username == request.Username {
			writeError(w, http.StatusConflict, errors.New("SMB username already exists"))
			return
		}
	}
	if _, err := exec.LookPath("smbpasswd"); err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Samba is not installed"))
		return
	}
	if err := s.run.Run(r.Context(), "useradd", []string{"--system", "--no-create-home", "--no-log-init", "--shell", "/usr/sbin/nologin", request.Username}, ""); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("create isolated SMB system account: %w", err))
		return
	}
	passwordInput := request.Password + "\n" + request.Password + "\n"
	if err := s.run.Run(r.Context(), "smbpasswd", []string{"-s", "-a", request.Username}, passwordInput); err != nil {
		_ = s.run.Run(context.Background(), "userdel", []string{request.Username}, "")
		writeError(w, http.StatusInternalServerError, fmt.Errorf("create Samba credentials: %w", err))
		return
	}
	user := SMBUser{Username: request.Username, CreatedAt: time.Now().UTC()}
	state.Users = append(state.Users, user)
	if err := s.saveAndApply(r.Context(), state); err != nil {
		_ = s.run.Run(context.Background(), "smbpasswd", []string{"-x", request.Username}, "")
		_ = s.run.Run(context.Background(), "userdel", []string{request.Username}, "")
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Service) updateSMBPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	username := r.PathValue("username")
	if !smbUsernamePattern.MatchString(username) || !validPassword(request.Password) {
		writeError(w, http.StatusBadRequest, errors.New("invalid SMB username or password"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !hasSMBUser(state.Users, username) {
		http.NotFound(w, r)
		return
	}
	if err := s.run.Run(r.Context(), "smbpasswd", []string{"-s", username}, request.Password+"\n"+request.Password+"\n"); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("update Samba credentials: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) deleteSMBUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	if !smbUsernamePattern.MatchString(username) {
		writeError(w, http.StatusBadRequest, errors.New("invalid SMB username"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !hasSMBUser(state.Users, username) {
		http.NotFound(w, r)
		return
	}
	for _, share := range state.Shares {
		for _, permission := range share.Permissions {
			if permission.Username == username {
				writeError(w, http.StatusConflict, errors.New("remove this account from all shares before deleting it"))
				return
			}
		}
	}
	if err := s.run.Run(r.Context(), "smbpasswd", []string{"-x", username}, ""); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("remove Samba credentials: %w", err))
		return
	}
	if err := s.run.Run(r.Context(), "userdel", []string{username}, ""); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("remove isolated SMB system account: %w", err))
		return
	}
	for i, user := range state.Users {
		if user.Username == username {
			state.Users = append(state.Users[:i], state.Users[i+1:]...)
			break
		}
	}
	if err := s.saveAndApply(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) saveAndApply(ctx context.Context, state persistedState) error {
	oldState, err := s.readState()
	if err != nil {
		return err
	}
	if err := s.save(state); err != nil {
		return err
	}
	if err := s.syncSamba(ctx, state); err != nil {
		if restoreErr := s.save(oldState); restoreErr != nil {
			return fmt.Errorf("%v; rollback state failed: %w", err, restoreErr)
		}
		if restoreErr := s.syncSamba(context.Background(), oldState); restoreErr != nil {
			return fmt.Errorf("%v; rollback Samba configuration failed: %w", err, restoreErr)
		}
		return err
	}
	return nil
}

func (s *Service) syncSamba(ctx context.Context, state persistedState) error {
	if _, err := exec.LookPath("testparm"); err != nil {
		return errors.New("Samba is not installed")
	}
	if err := ensureDirectory(s.shareRoot); err != nil {
		return err
	}
	if err := ensureShareOwner(ctx, s.run); err != nil {
		return err
	}
	fragment := renderSambaConfig(state.Shares)
	previousFragment, fragmentExists, err := readOptionalFile(s.fragmentPath)
	if err != nil {
		return err
	}
	previousMain, mainExists, err := readOptionalFile(s.configPath)
	if err != nil {
		return err
	}
	updatedMain, err := addSambaInclude(string(previousMain), s.fragmentPath)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.fragmentPath, []byte(fragment), 0644); err != nil {
		return fmt.Errorf("write ASNS Samba share configuration: %w", err)
	}
	if err := atomicWrite(s.configPath, []byte(updatedMain), 0644); err != nil {
		_ = restoreFile(s.fragmentPath, previousFragment, fragmentExists)
		return fmt.Errorf("enable ASNS Samba include: %w", err)
	}
	if err := s.run.Run(ctx, "testparm", []string{"-s", s.configPath}, ""); err != nil {
		_ = restoreFile(s.fragmentPath, previousFragment, fragmentExists)
		_ = restoreFile(s.configPath, previousMain, mainExists)
		return fmt.Errorf("Samba rejected the generated configuration: %w", err)
	}
	if err := s.run.Run(ctx, "systemctl", []string{"reload-or-restart", "smbd.service"}, ""); err != nil {
		_ = restoreFile(s.fragmentPath, previousFragment, fragmentExists)
		_ = restoreFile(s.configPath, previousMain, mainExists)
		_ = s.run.Run(context.Background(), "systemctl", []string{"reload-or-restart", "smbd.service"}, "")
		return fmt.Errorf("reload Samba configuration: %w", err)
	}
	return nil
}

func ensureShareOwner(ctx context.Context, runner commandRunner) error {
	if _, err := user.Lookup("asns-share"); err == nil {
		return nil
	} else if _, ok := err.(user.UnknownUserError); !ok {
		return fmt.Errorf("look up restricted Samba file owner: %w", err)
	}
	if err := runner.Run(ctx, "useradd", []string{"--system", "--no-create-home", "--no-log-init", "--shell", "/usr/sbin/nologin", "asns-share"}, ""); err != nil {
		if _, lookupErr := user.Lookup("asns-share"); lookupErr != nil {
			return fmt.Errorf("create restricted Samba file owner: %w", err)
		}
	}
	return nil
}

func (s *Service) load() (persistedState, error) {
	return s.readState()
}

func (s *Service) readState() (persistedState, error) {
	data, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return persistedState{Shares: []Share{}, Users: []SMBUser{}}, nil
	}
	if err != nil {
		return persistedState{}, fmt.Errorf("read ASNS Agent state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedState{}, fmt.Errorf("decode ASNS Agent state: %w", err)
	}
	return state, nil
}

func (s *Service) save(state persistedState) error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0700); err != nil {
		return fmt.Errorf("create ASNS Agent state directory: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode ASNS Agent state: %w", err)
	}
	if err := atomicWrite(s.statePath, data, 0600); err != nil {
		return fmt.Errorf("save ASNS Agent state: %w", err)
	}
	return nil
}

func renderSambaConfig(shares []Share) string {
	var output strings.Builder
	for _, share := range shares {
		output.WriteString("\n[")
		output.WriteString(share.Name)
		output.WriteString("]\n")
		output.WriteString("    path = ")
		output.WriteString(share.Path)
		output.WriteString("\n    browseable = yes\n    guest ok = no\n    read only = yes\n")
		output.WriteString("    force user = asns-share\n    create mask = 0660\n    directory mask = 2770\n")
		valid := make([]string, 0, len(share.Permissions))
		writable := make([]string, 0, len(share.Permissions))
		for _, permission := range share.Permissions {
			valid = append(valid, permission.Username)
			if permission.Access == "write" {
				writable = append(writable, permission.Username)
			}
		}
		output.WriteString("    valid users = ")
		output.WriteString(strings.Join(valid, " "))
		output.WriteByte('\n')
		if len(writable) > 0 {
			output.WriteString("    write list = ")
			output.WriteString(strings.Join(writable, " "))
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func addSambaInclude(existing, fragment string) (string, error) {
	include := "    include = " + fragment
	if strings.Contains(existing, include) {
		return existing, nil
	}
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	globalIndex := -1
	for index, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "[global]") {
			globalIndex = index
			break
		}
	}
	if globalIndex < 0 {
		if strings.TrimSpace(existing) != "" {
			return "", errors.New("Samba configuration has no [global] section")
		}
		return "[global]\n" + include + "\n", nil
	}
	insertAt := globalIndex + 1
	for insertAt < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[insertAt]), "[") {
		insertAt++
	}
	lines = append(lines, "")
	copy(lines[insertAt+1:], lines[insertAt:])
	lines[insertAt] = include
	return strings.Join(lines, "\n"), nil
}

func removeSambaInclude(existing, fragment string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	inGlobal := false
	changed := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inGlobal = strings.EqualFold(trimmed, "[global]")
		}
		if inGlobal && trimmed == "include = "+fragment {
			lines[index] = ""
			changed = true
		}
	}
	if !changed {
		return existing, false
	}
	return strings.Join(lines, "\n"), true
}

func validatePermissions(permissions []Permission, users []SMBUser) error {
	if len(permissions) == 0 {
		return errors.New("a share must grant access to at least one SMB account")
	}
	seen := make(map[string]bool)
	for _, permission := range permissions {
		if !hasSMBUser(users, permission.Username) {
			return fmt.Errorf("unknown SMB account %q", permission.Username)
		}
		if permission.Access != "read" && permission.Access != "write" {
			return errors.New("share access must be read or write")
		}
		if seen[permission.Username] {
			return errors.New("duplicate SMB account permission")
		}
		seen[permission.Username] = true
	}
	return nil
}

func hasSMBUser(users []SMBUser, username string) bool {
	for _, user := range users {
		if user.Username == username {
			return true
		}
	}
	return false
}

func findShare(shares []Share, id string) int {
	for index, share := range shares {
		if share.ID == id {
			return index
		}
	}
	return -1
}

func validPassword(password string) bool {
	if len(password) < 12 || len(password) > 72 {
		return false
	}
	for _, character := range password {
		if character == '\n' || character == '\r' || character == 0 {
			return false
		}
	}
	return true
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0750); err != nil {
		return fmt.Errorf("create managed Samba directory: %w", err)
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect managed Samba directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("managed Samba paths cannot contain symlinks or non-directories")
		}
	}
	return nil
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read Samba configuration: %w", err)
	}
	return data, true, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".asns-")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func restoreFile(path string, previous []byte, existed bool) error {
	if !existed {
		return os.Remove(path)
	}
	return atomicWrite(path, previous, 0644)
}
