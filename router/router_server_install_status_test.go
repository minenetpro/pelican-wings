package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Minenetpro/pelican-wings/config"
	"github.com/Minenetpro/pelican-wings/server"
)

type installStatusTestResponse struct {
	IsInstalling   bool   `json:"is_installing"`
	IsTransferring bool   `json:"is_transferring"`
	IsRestoring    bool   `json:"is_restoring"`
	ServerState    string `json:"server_state"`
	InstallLog     struct {
		Exists    bool       `json:"exists"`
		SizeBytes int64      `json:"size_bytes"`
		UpdatedAt *time.Time `json:"updated_at"`
	} `json:"install_log"`
}

func makeInstallStatusTestServer(t *testing.T, uuid string) *server.Server {
	t.Helper()

	s, err := server.New(nil)
	if err != nil {
		t.Fatalf("failed to create test server: %v", err)
	}

	s.Config().Uuid = uuid
	return s
}

func performInstallStatusRequest(t *testing.T, s *server.Server) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/servers/"+s.ID()+"/install-status", nil)
	c.Set("server", s)

	getServerInstallStatus(c)
	return w
}

func TestGetServerInstallStatus_WithInstallLog(t *testing.T) {
	logDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System:              config.SystemConfiguration{LogDirectory: logDir},
	})

	s := makeInstallStatusTestServer(t, "server-install-status-with-log")
	s.SetTransferring(true)
	s.SetRestoring(true)

	installDir := filepath.Join(logDir, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("failed creating install dir: %v", err)
	}

	contents := []byte("install output line")
	logFile := filepath.Join(installDir, s.ID()+".log")
	if err := os.WriteFile(logFile, contents, 0o644); err != nil {
		t.Fatalf("failed writing install log file: %v", err)
	}

	modTime := time.Date(2026, time.February, 16, 19, 14, 22, 0, time.UTC)
	if err := os.Chtimes(logFile, modTime, modTime); err != nil {
		t.Fatalf("failed updating modtime: %v", err)
	}

	w := performInstallStatusRequest(t, s)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var resp installStatusTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding response JSON: %v", err)
	}

	if resp.IsInstalling {
		t.Fatalf("expected is_installing=false")
	}
	if !resp.IsTransferring {
		t.Fatalf("expected is_transferring=true")
	}
	if !resp.IsRestoring {
		t.Fatalf("expected is_restoring=true")
	}
	if resp.ServerState != "offline" {
		t.Fatalf("expected server_state=offline, got %q", resp.ServerState)
	}
	if !resp.InstallLog.Exists {
		t.Fatalf("expected install_log.exists=true")
	}
	if resp.InstallLog.SizeBytes != int64(len(contents)) {
		t.Fatalf("expected size_bytes=%d, got %d", len(contents), resp.InstallLog.SizeBytes)
	}
	if resp.InstallLog.UpdatedAt == nil {
		t.Fatalf("expected updated_at to be present")
	}
	if resp.InstallLog.UpdatedAt.Before(modTime.Add(-2*time.Second)) || resp.InstallLog.UpdatedAt.After(modTime.Add(2*time.Second)) {
		t.Fatalf("expected updated_at near %s, got %s", modTime.Format(time.RFC3339), resp.InstallLog.UpdatedAt.Format(time.RFC3339))
	}
}

func TestGetServerInstallStatus_MissingInstallLog(t *testing.T) {
	logDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System:              config.SystemConfiguration{LogDirectory: logDir},
	})

	s := makeInstallStatusTestServer(t, "server-install-status-missing-log")

	w := performInstallStatusRequest(t, s)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var resp installStatusTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding response JSON: %v", err)
	}

	if resp.InstallLog.Exists {
		t.Fatalf("expected install_log.exists=false")
	}
	if resp.InstallLog.SizeBytes != 0 {
		t.Fatalf("expected size_bytes=0, got %d", resp.InstallLog.SizeBytes)
	}
	if resp.InstallLog.UpdatedAt != nil {
		t.Fatalf("expected updated_at=nil")
	}
}

func TestGetServerInstallStatus_InstallLogMetadataError(t *testing.T) {
	root := t.TempDir()
	badPath := filepath.Join(root, "log-dir-file")
	if err := os.WriteFile(badPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("failed writing setup file: %v", err)
	}

	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System:              config.SystemConfiguration{LogDirectory: badPath},
	})

	s := makeInstallStatusTestServer(t, "server-install-status-stat-error")

	w := performInstallStatusRequest(t, s)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d; body=%s", w.Code, w.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed decoding error response JSON: %v", err)
	}
	if body["error"] == "" {
		t.Fatalf("expected error message in response body")
	}
}
