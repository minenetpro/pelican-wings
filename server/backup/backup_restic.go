package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"emperror.dev/errors"

	"github.com/Minenetpro/pelican-wings/config"
	"github.com/Minenetpro/pelican-wings/remote"
	"github.com/Minenetpro/pelican-wings/server/filesystem"
)

type ResticBackup struct {
	Backup
	resticConfig *ResticRuntimeConfig
}

var _ BackupInterface = (*ResticBackup)(nil)

// repoInitMu protects repository initialization per repository key.
var repoInitMu sync.Map

type ResticRuntimeConfig struct {
	RepositoryKey      string `json:"repository_key"`
	Repository         string `json:"repository"`
	Password           string `json:"password"`
	AWSAccessKeyID     string `json:"aws_access_key_id"`
	AWSSecretAccessKey string `json:"aws_secret_access_key"`
	AWSSessionToken    string `json:"aws_session_token"`
	AWSRegion          string `json:"aws_region"`
}

// resticSnapshot represents a snapshot from restic snapshots output.
type resticSnapshot struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"short_id"`
	Time     string   `json:"time"`
	Hostname string   `json:"hostname"`
	Tags     []string `json:"tags"`
	Paths    []string `json:"paths"`
}

// resticStats represents the stats output from restic stats command.
type resticStats struct {
	TotalSize      int64 `json:"total_size"`
	TotalFileCount int64 `json:"total_file_count"`
}

func NewRestic(client remote.Client, uuid string, suuid string, ignore string, resticConfig *ResticRuntimeConfig) *ResticBackup {
	return &ResticBackup{
		Backup: Backup{
			client:     client,
			Uuid:       uuid,
			ServerUuid: suuid,
			Ignore:     ignore,
			adapter:    ResticBackupAdapter,
		},
		resticConfig: resticConfig,
	}
}

// WithLogContext attaches additional context to the log output for this backup.
func (r *ResticBackup) WithLogContext(c map[string]interface{}) {
	r.logContext = c
}

// SkipPanelNotification returns true as restic backups are managed externally.
func (r *ResticBackup) SkipPanelNotification() bool {
	return true
}

// Remove removes a backup snapshot from the restic repository.
func (r *ResticBackup) Remove() error {
	ctx := context.Background()

	r.log().Info("removing backup snapshot from restic repository")

	// Find the snapshot first
	snapshot, err := r.findSnapshotByTag(ctx)
	if err != nil {
		return errors.Wrap(err, "backup: failed to find snapshot to remove")
	}

	if snapshot == nil {
		r.log().Warn("no snapshot found with the specified backup_uuid, nothing to remove")
		return nil
	}

	// Build forget command arguments with specific snapshot ID
	args := []string{"forget", snapshot.ID, "--prune"}

	// Add cache directory if configured
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	// Use forget with specific snapshot ID to remove only this snapshot
	// The --prune flag removes unreferenced data from the repository
	_, err = r.runRestic(ctx, args...)
	if err != nil {
		return errors.Wrap(err, "backup: failed to remove restic snapshot")
	}

	r.log().Info("successfully removed backup snapshot from restic repository")
	return nil
}

// Generate creates a backup of the server's files using restic.
func (r *ResticBackup) Generate(ctx context.Context, _ *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	// Build the source path for this server's data
	sourcePath := filepath.Join(config.Get().System.Data, r.ServerUuid)

	r.log().WithField("path", sourcePath).Info("creating restic backup for server")

	// Ensure the repository exists (auto-init if needed)
	if err := r.ensureRepository(ctx); err != nil {
		return nil, errors.Wrap(err, "backup: failed to ensure restic repository exists")
	}

	// Build restic backup command arguments
	args := []string{
		"backup",
		"--host", r.ServerUuid,
		"--tag", r.backupTag(),
		"--tag", r.serverTag(),
	}

	// Add exclusion patterns if provided
	if ignore != "" {
		for _, pattern := range strings.Split(ignore, "\n") {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" && !strings.HasPrefix(pattern, "#") {
				args = append(args, "--exclude", pattern)
			}
		}
	}

	// Add cache directory if configured
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	// Add the source path
	args = append(args, sourcePath)

	// Execute the backup
	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return nil, errors.Wrap(err, "backup: failed to create restic backup")
	}

	r.log().WithField("output", string(output)).Debug("restic backup output")
	r.log().Info("successfully created restic backup")

	return &ArchiveDetails{
		Checksum:     "",
		ChecksumType: "none",
		Size:         0,
		Parts:        nil,
	}, nil
}

// Restore restores a backup from the restic repository to the server's data directory.
// This supports cross-server restore by using the snapshotID:path syntax to restore
// files from the original server's path directly into the target server's directory.
func (r *ResticBackup) Restore(ctx context.Context, _ io.Reader, callback RestoreCallback) error {
	// Find the snapshot for this backup_uuid
	snapshot, err := r.findSnapshotByTag(ctx)
	if err != nil {
		return errors.Wrap(err, "backup: failed to find restic snapshot")
	}

	if snapshot == nil {
		return errors.New("backup: no snapshot found with the specified backup_uuid")
	}

	// Determine the source path from the snapshot (the original server's data directory)
	if len(snapshot.Paths) == 0 {
		return errors.New("backup: snapshot has no paths to restore")
	}
	sourcePath := snapshot.Paths[0]

	// Target path is the current server's data directory
	targetPath := filepath.Join(config.Get().System.Data, r.ServerUuid)

	r.log().WithField("snapshot", snapshot.ID).
		WithField("source", sourcePath).
		WithField("target", targetPath).
		Info("restoring restic backup")

	// Use snapshotID:path syntax to restore contents directly to target.
	// This allows cross-server restore without nested directories.
	// See: https://restic.readthedocs.io/en/latest/050_restore.html
	snapshotWithPath := fmt.Sprintf("%s:%s", snapshot.ID, sourcePath)

	args := []string{
		"restore",
		snapshotWithPath,
		"--target", targetPath,
	}

	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return errors.Wrap(err, "backup: failed to restore restic backup")
	}

	r.log().WithField("output", string(output)).Debug("restic restore output")
	r.log().Info("successfully restored restic backup")

	return nil
}

// Path returns an empty string as restic backups are not stored locally.
func (r *ResticBackup) Path() string {
	return ""
}

// Checksum returns nil as restic handles checksums internally.
func (r *ResticBackup) Checksum() ([]byte, error) {
	return nil, nil
}

// Size returns 0 as the size is not tracked locally for restic backups.
func (r *ResticBackup) Size() (int64, error) {
	return 0, nil
}

// Details returns minimal archive details for restic backups.
func (r *ResticBackup) Details(ctx context.Context, parts []remote.BackupPart) (*ArchiveDetails, error) {
	return &ArchiveDetails{
		Checksum:     "",
		ChecksumType: "none",
		Size:         0,
		Parts:        parts,
	}, nil
}

// backupTag returns the tag used to identify this specific backup.
func (r *ResticBackup) backupTag() string {
	return fmt.Sprintf("backup_uuid:%s", r.Uuid)
}

// serverTag returns the tag used to identify the server.
func (r *ResticBackup) serverTag() string {
	return fmt.Sprintf("server_uuid:%s", r.ServerUuid)
}

func (r *ResticBackup) effectiveResticConfig() ResticRuntimeConfig {
	if r.resticConfig == nil {
		return ResticRuntimeConfig{}
	}

	return *r.resticConfig
}

func (r *ResticBackup) validateResticConfig() error {
	cfg := r.effectiveResticConfig()
	if cfg.Repository == "" {
		return errors.New("backup: restic repository is not configured")
	}
	if cfg.Password == "" {
		return errors.New("backup: restic password is not configured")
	}
	return nil
}

func (r *ResticBackup) repositoryKey() string {
	cfg := r.effectiveResticConfig()
	key := cfg.RepositoryKey
	if key == "" {
		key = cfg.Repository
	}
	if key == "" {
		return "default"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (r *ResticBackup) cacheDir() string {
	base := config.Get().System.Backups.Restic.CacheDir
	if base == "" {
		return ""
	}
	return filepath.Join(base, r.repositoryKey())
}

func (r *ResticBackup) initLock() *sync.Mutex {
	lock, _ := repoInitMu.LoadOrStore(r.repositoryKey(), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// buildEnv builds the environment variables for restic commands.
func (r *ResticBackup) buildEnv() []string {
	cfg := r.effectiveResticConfig()

	env := os.Environ()
	env = append(env,
		fmt.Sprintf("RESTIC_REPOSITORY=%s", cfg.Repository),
		fmt.Sprintf("RESTIC_PASSWORD=%s", cfg.Password),
	)

	if cfg.AWSAccessKeyID != "" {
		env = append(env, fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", cfg.AWSAccessKeyID))
	}
	if cfg.AWSSecretAccessKey != "" {
		env = append(env, fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", cfg.AWSSecretAccessKey))
	}
	if cfg.AWSSessionToken != "" {
		env = append(env, fmt.Sprintf("AWS_SESSION_TOKEN=%s", cfg.AWSSessionToken))
	}
	if cfg.AWSRegion != "" {
		env = append(env, fmt.Sprintf("AWS_DEFAULT_REGION=%s", cfg.AWSRegion))
		env = append(env, fmt.Sprintf("AWS_REGION=%s", cfg.AWSRegion))
	}

	return env
}

// runRestic executes a restic command with the appropriate environment.
func (r *ResticBackup) runRestic(ctx context.Context, args ...string) ([]byte, error) {
	if err := r.validateResticConfig(); err != nil {
		return nil, err
	}

	cfg := config.Get().System.Backups.Restic

	cmd := exec.CommandContext(ctx, cfg.BinaryPath, args...)
	cmd.Env = r.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	r.log().WithField("command", fmt.Sprintf("%s %s", cfg.BinaryPath, strings.Join(args, " "))).Debug("executing restic command")

	if err := cmd.Run(); err != nil {
		r.log().WithField("stderr", stderr.String()).WithField("stdout", stdout.String()).Error("restic command failed")
		return nil, errors.Wrap(err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// ensureRepository checks if the repository exists and initializes it if needed.
func (r *ResticBackup) ensureRepository(ctx context.Context) error {
	// Use mutex to prevent concurrent initialization attempts
	lock := r.initLock()
	lock.Lock()
	defer lock.Unlock()

	// Try to list snapshots to check if repo exists
	args := []string{"snapshots", "--json"}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	_, err := r.runRestic(ctx, args...)
	if err == nil {
		// Repository exists
		return nil
	}

	// Check if the error indicates the repository doesn't exist
	errStr := err.Error()
	if !strings.Contains(errStr, "repository does not exist") &&
		!strings.Contains(errStr, "Is there a repository at") &&
		!strings.Contains(errStr, "unable to open config file") {
		// Some other error occurred
		return err
	}

	r.log().Info("restic repository does not exist, initializing...")

	// Initialize the repository
	initArgs := []string{"init"}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		initArgs = append(initArgs, "--cache-dir", cacheDir)
	}

	_, err = r.runRestic(ctx, initArgs...)
	if err != nil {
		return errors.Wrap(err, "backup: failed to initialize restic repository")
	}

	r.log().Info("successfully initialized restic repository")
	return nil
}

// SnapshotInfo represents snapshot data for API responses.
type SnapshotInfo struct {
	ID             string   `json:"id"`
	ShortID        string   `json:"short_id"`
	Time           string   `json:"time"`
	BackupUUID     string   `json:"backup_uuid"`
	ServerUUID     string   `json:"server_uuid"`
	Paths          []string `json:"paths"`
	Size           *int64   `json:"size,omitempty"`
	TotalFileCount *int64   `json:"total_file_count,omitempty"`
}

// parseSnapshotToInfo converts a resticSnapshot to SnapshotInfo, extracting UUIDs from tags.
func parseSnapshotToInfo(snapshot resticSnapshot) SnapshotInfo {
	info := SnapshotInfo{
		ID:      snapshot.ID,
		ShortID: snapshot.ShortID,
		Time:    snapshot.Time,
		Paths:   snapshot.Paths,
	}
	for _, tag := range snapshot.Tags {
		if strings.HasPrefix(tag, "backup_uuid:") {
			info.BackupUUID = strings.TrimPrefix(tag, "backup_uuid:")
		}
		if strings.HasPrefix(tag, "server_uuid:") {
			info.ServerUUID = strings.TrimPrefix(tag, "server_uuid:")
		}
	}
	return info
}

// GetSnapshotStats retrieves size statistics for a specific snapshot.
func (r *ResticBackup) GetSnapshotStats(ctx context.Context, snapshotID string) (*resticStats, error) {
	args := []string{"stats", "--json", snapshotID}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return nil, err
	}

	var stats resticStats
	if err := json.Unmarshal(output, &stats); err != nil {
		return nil, errors.Wrap(err, "backup: failed to parse restic stats output")
	}

	return &stats, nil
}

// EnrichSnapshotWithStats adds size information to a SnapshotInfo.
func (r *ResticBackup) EnrichSnapshotWithStats(ctx context.Context, info *SnapshotInfo) {
	stats, err := r.GetSnapshotStats(ctx, info.ID)
	if err != nil {
		r.log().WithField("snapshot", info.ID).Debug("could not retrieve snapshot stats")
		return
	}
	info.Size = &stats.TotalSize
	info.TotalFileCount = &stats.TotalFileCount
}

// ListSnapshots returns all snapshots for this server from the restic repository.
// If includeStats is true, it will also fetch size information for each snapshot
// (this is slower as it requires an additional restic command per snapshot).
func (r *ResticBackup) ListSnapshots(ctx context.Context, includeStats bool) ([]SnapshotInfo, error) {
	args := []string{"snapshots", "--json", "--tag", r.serverTag()}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return nil, errors.Wrap(err, "backup: failed to list restic snapshots")
	}

	var snapshots []resticSnapshot
	if err := json.Unmarshal(output, &snapshots); err != nil {
		return nil, errors.Wrap(err, "backup: failed to parse restic snapshots output")
	}

	result := make([]SnapshotInfo, 0, len(snapshots))
	for _, s := range snapshots {
		info := parseSnapshotToInfo(s)
		if includeStats {
			r.EnrichSnapshotWithStats(ctx, &info)
		}
		result = append(result, info)
	}

	return result, nil
}

// GetSnapshotStatus checks if a snapshot exists for this backup and returns its info.
func (r *ResticBackup) GetSnapshotStatus(ctx context.Context, includeStats bool) (*SnapshotInfo, error) {
	args := []string{"snapshots", "--json", "--tag", r.backupTag()}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return nil, errors.Wrap(err, "backup: failed to get restic snapshot status")
	}

	var snapshots []resticSnapshot
	if err := json.Unmarshal(output, &snapshots); err != nil {
		return nil, errors.Wrap(err, "backup: failed to parse restic snapshots output")
	}

	if len(snapshots) == 0 {
		return nil, nil
	}

	info := parseSnapshotToInfo(snapshots[0])
	if includeStats {
		r.EnrichSnapshotWithStats(ctx, &info)
	}
	return &info, nil
}

// findSnapshotByTag finds a snapshot by its backup_uuid tag and returns full info.
func (r *ResticBackup) findSnapshotByTag(ctx context.Context) (*SnapshotInfo, error) {
	args := []string{"snapshots", "--json", "--tag", r.backupTag()}
	if cacheDir := r.cacheDir(); cacheDir != "" {
		args = append(args, "--cache-dir", cacheDir)
	}

	output, err := r.runRestic(ctx, args...)
	if err != nil {
		return nil, err
	}

	var snapshots []resticSnapshot
	if err := json.Unmarshal(output, &snapshots); err != nil {
		return nil, errors.Wrap(err, "backup: failed to parse restic snapshots output")
	}

	if len(snapshots) == 0 {
		return nil, nil
	}

	// Return the first (and should be only) matching snapshot
	info := parseSnapshotToInfo(snapshots[0])
	return &info, nil
}
