package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testCommandRunner struct {
	calls    []string
	failName string
}

func (r *testCommandRunner) Run(_ context.Context, name string, args []string, _ string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == r.failName {
		return errors.New("injected command failure")
	}
	return nil
}

func (r *testCommandRunner) Output(_ context.Context, name string, args []string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == r.failName {
		return nil, errors.New("injected command failure")
	}
	return []byte("MD_STATE=clean\n"), nil
}

func TestAgentOnlyReturnsPersistedShareAndAccountState(t *testing.T) {
	service := NewService(filepath.Join(t.TempDir(), "state.json"), filepath.Join(t.TempDir(), "shares"),
		filepath.Join(t.TempDir(), "smb.conf"), filepath.Join(t.TempDir(), "asns-shares.conf"))
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/shares", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list shares status = %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Shares []Share `json:"shares"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Shares == nil || len(result.Shares) != 0 {
		t.Fatalf("expected an empty persisted share list, got %+v", result.Shares)
	}
}

func TestSambaPermissionsAndNamesAreRestricted(t *testing.T) {
	users := []SMBUser{{Username: "asns_media"}}
	if err := validatePermissions([]Permission{{Username: "asns_media", Access: "write"}}, users); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]Permission{
		nil,
		{{Username: "unmanaged", Access: "write"}},
		{{Username: "asns_media", Access: "admin"}},
		{{Username: "asns_media", Access: "read"}, {Username: "asns_media", Access: "write"}},
	} {
		if err := validatePermissions(invalid, users); err == nil {
			t.Fatalf("expected invalid permissions to be rejected: %+v", invalid)
		}
	}
	if shareNamePattern.MatchString("share]\ninclude = /etc/passwd") {
		t.Fatal("share name validation accepts Samba configuration injection")
	}
	if smbUsernamePattern.MatchString("root") || smbUsernamePattern.MatchString("asns_") {
		t.Fatal("SMB account name validation accepted an unsafe name")
	}
}

func TestRenderSambaConfigRestrictsGuestAndWriteAccess(t *testing.T) {
	config := renderSambaConfig([]Share{{
		Name: "Media", Path: "/srv/asns/shares/abc",
		Permissions: []Permission{{Username: "asns_read", Access: "read"}, {Username: "asns_write", Access: "write"}},
	}})
	for _, expected := range []string{
		"[Media]", "guest ok = no", "read only = yes",
		"valid users = asns_read asns_write", "write list = asns_write", "force user = asns-share",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("Samba share configuration lacks %q:\n%s", expected, config)
		}
	}
}

func TestSambaIncludeIsOnlyAddedInGlobalSection(t *testing.T) {
	config, err := addSambaInclude("[global]\n   workgroup = WORKGROUP\n\n[homes]\n   read only = no\n",
		"/etc/samba/asns-shares.conf")
	if err != nil {
		t.Fatal(err)
	}
	global := strings.Index(config, "[global]")
	include := strings.Index(config, "include = /etc/samba/asns-shares.conf")
	homes := strings.Index(config, "[homes]")
	if global < 0 || include < global || homes < include {
		t.Fatalf("include was not inserted into the global section:\n%s", config)
	}
}

func TestParseMDStatReportsHealthyDegradedAndSyncingArrays(t *testing.T) {
	input := `Personalities : [raid1] [raid5]
md0 : active raid1 sda1[0] sdb1[1]
      976630336 blocks super 1.2 [2/2] [UU]

md1 : active raid5 sdc1[0] sdd1[1](F) sde1[2]
      1953260544 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [UU_]
      [===>.................]  recovery = 17.4% (123/456) finish=1.2min speed=100K/sec

md2 : inactive sff1[0]
      1000 blocks
`
	arrays, err := parseMDStat([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(arrays) != 3 {
		t.Fatalf("got %d arrays, want 3: %+v", len(arrays), arrays)
	}
	if arrays[0].Name != "md0" || arrays[0].Level != "raid1" || arrays[0].State != "healthy" ||
		arrays[0].ActiveDevices != 2 || arrays[0].RaidDevices != 2 {
		t.Fatalf("unexpected healthy array status: %+v", arrays[0])
	}
	if arrays[1].Name != "md1" || arrays[1].Level != "raid5" || arrays[1].State != "degraded" ||
		arrays[1].ActiveDevices != 2 || arrays[1].RaidDevices != 3 || arrays[1].SyncAction != "recovery" ||
		arrays[1].SyncPercent == nil || *arrays[1].SyncPercent != 17.4 {
		t.Fatalf("unexpected degraded/recovery array status: %+v", arrays[1])
	}
	if arrays[2].Name != "md2" || arrays[2].Level != "unknown" || arrays[2].State != "inactive" {
		t.Fatalf("unexpected inactive array status: %+v", arrays[2])
	}
}

func TestRaidStatusUsesOnlyReadOnlyMdadmDetail(t *testing.T) {
	root := t.TempDir()
	mdstat := filepath.Join(root, "mdstat")
	if err := os.WriteFile(mdstat, []byte("md0 : active raid1 sda1[0] sdb1[1]\n      1000 blocks [2/2] [UU]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(root, "state.json"), filepath.Join(root, "shares"),
		filepath.Join(root, "smb.conf"), filepath.Join(root, "asns-shares.conf"))
	service.mdstatPath = mdstat
	runner := &testCommandRunner{}
	service.run = runner

	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/raid", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("raid status returned %d: %s", response.Code, response.Body.String())
	}
	if len(runner.calls) != 1 || runner.calls[0] != "mdadm --detail --export /dev/md0" {
		t.Fatalf("unexpected mdadm command: %v", runner.calls)
	}
	var result struct {
		Arrays []RAIDArray `json:"arrays"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Arrays) != 1 || result.Arrays[0].MdadmState != "clean" {
		t.Fatalf("unexpected RAID response: %+v", result)
	}
}

func TestRemoveSambaIntegrationPreservesUnrelatedConfiguration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "smb.conf")
	fragmentPath := filepath.Join(root, "asns-shares.conf")
	config := "[global]\n   workgroup = WORKGROUP\n    include = " + fragmentPath + "\n\n[homes]\n    include = /etc/samba/other.conf\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte("[Media]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(root, "state.json"), filepath.Join(root, "shares"), configPath, fragmentPath)
	runner := &testCommandRunner{}
	service.run = runner

	if err := service.RemoveSambaIntegration(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), "include = "+fragmentPath) ||
		!strings.Contains(string(updated), "include = /etc/samba/other.conf") {
		t.Fatalf("cleanup did not remove only the ASNS global include:\n%s", updated)
	}
	if _, err := os.Stat(fragmentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ASNS Samba fragment still exists: %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0] != "testparm -s "+configPath ||
		runner.calls[1] != "systemctl try-reload-or-restart smbd.service" {
		t.Fatalf("unexpected cleanup commands: %v", runner.calls)
	}
}

func TestRemoveSambaIntegrationRestoresFilesWhenConfigurationIsRejected(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "smb.conf")
	fragmentPath := filepath.Join(root, "asns-shares.conf")
	config := "[global]\n    include = " + fragmentPath + "\n"
	fragment := "[Media]\n    path = /srv/asns/shares/media\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte(fragment), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(root, "state.json"), filepath.Join(root, "shares"), configPath, fragmentPath)
	service.run = &testCommandRunner{failName: "testparm"}

	if err := service.RemoveSambaIntegration(context.Background()); err == nil {
		t.Fatal("expected invalid Samba configuration to fail cleanup")
	}
	restoredConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredFragment, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredConfig) != config || string(restoredFragment) != fragment {
		t.Fatalf("failed cleanup did not restore the previous files: config=%q fragment=%q", restoredConfig, restoredFragment)
	}
}
