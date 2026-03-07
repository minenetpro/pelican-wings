package router

import (
	"net/http"
	"os"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/Minenetpro/pelican-wings/config"
	"github.com/Minenetpro/pelican-wings/router/middleware"
	"github.com/Minenetpro/pelican-wings/server"
	"github.com/Minenetpro/pelican-wings/server/backup"
)

type resticQueryBody struct {
	IncludeStats bool                        `json:"include_stats"`
	ResticConfig *backup.ResticRuntimeConfig `json:"restic_config"`
}

func validateResticRequestConfig(resticConfig *backup.ResticRuntimeConfig) error {
	if resticConfig == nil {
		return errors.New("router/backups: restic_config is required for restic backup operations")
	}

	if strings.TrimSpace(resticConfig.Repository) == "" {
		return errors.New("router/backups: restic_config.repository is required for restic backup operations")
	}

	if strings.TrimSpace(resticConfig.Password) == "" {
		return errors.New("router/backups: restic_config.password is required for restic backup operations")
	}

	return nil
}

func abortLegacyResticGet(c *gin.Context) {
	c.Header("Allow", http.MethodPost)
	c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{
		"error": "Restic snapshot lookup requires POST with restic_config.",
	})
}

// postServerBackup performs a backup against a given server instance using the
// provided backup adapter.
func postServerBackup(c *gin.Context) {
	s := middleware.ExtractServer(c)
	client := middleware.ExtractApiClient(c)
	logger := middleware.ExtractLogger(c)
	var data struct {
		Adapter      backup.AdapterType          `json:"adapter"`
		Uuid         string                      `json:"uuid"`
		Ignore       string                      `json:"ignore"`
		ResticConfig *backup.ResticRuntimeConfig `json:"restic_config"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}

	var adapter backup.BackupInterface
	switch data.Adapter {
	case backup.LocalBackupAdapter:
		adapter = backup.NewLocal(client, data.Uuid, s.ID(), data.Ignore)
	case backup.S3BackupAdapter:
		adapter = backup.NewS3(client, data.Uuid, s.ID(), data.Ignore)
	case backup.ResticBackupAdapter:
		if !config.Get().System.Backups.Restic.Enabled {
			middleware.CaptureAndAbort(c, errors.New("router/backups: restic backup adapter is not enabled"))
			return
		}
		if err := validateResticRequestConfig(data.ResticConfig); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		adapter = backup.NewRestic(client, data.Uuid, s.ID(), data.Ignore, data.ResticConfig)
	default:
		middleware.CaptureAndAbort(c, errors.New("router/backups: provided adapter is not valid: "+string(data.Adapter)))
		return
	}

	// Attach the server ID and the request ID to the adapter log context for easier
	// parsing in the logs.
	adapter.WithLogContext(map[string]interface{}{
		"server":     s.ID(),
		"request_id": c.GetString("request_id"),
	})

	go func(b backup.BackupInterface, s *server.Server, logger *log.Entry) {
		if err := s.Backup(b); err != nil {
			logger.WithField("error", errors.WithStackIf(err)).Error("router: failed to generate server backup")
		}
	}(adapter, s, logger)

	c.Status(http.StatusAccepted)
}

// postServerRestoreBackup handles restoring a backup for a server by downloading
// or finding the given backup on the system and then unpacking the archive into
// the server's data directory. If the TruncateDirectory field is provided and
// is true all of the files will be deleted for the server.
//
// This endpoint will block until the backup is fully restored allowing for a
// spinner to be displayed in the Panel UI effectively.
//
// TODO: stop the server if it is running
func postServerRestoreBackup(c *gin.Context) {
	s := middleware.ExtractServer(c)
	client := middleware.ExtractApiClient(c)
	logger := middleware.ExtractLogger(c)

	var data struct {
		Adapter           backup.AdapterType          `binding:"required,oneof=wings s3 restic" json:"adapter"`
		TruncateDirectory bool                        `json:"truncate_directory"`
		ResticConfig      *backup.ResticRuntimeConfig `json:"restic_config"`
		// A UUID is always required for this endpoint, however the download URL
		// is only present when the given adapter type is s3.
		DownloadUrl string `json:"download_url"`
	}
	if err := c.BindJSON(&data); err != nil {
		return
	}
	if data.Adapter == backup.S3BackupAdapter && data.DownloadUrl == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "The download_url field is required when the backup adapter is set to S3."})
		return
	}
	if data.Adapter == backup.ResticBackupAdapter && !config.Get().System.Backups.Restic.Enabled {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "The restic backup adapter is not enabled on this node."})
		return
	}
	if data.Adapter == backup.ResticBackupAdapter {
		if err := validateResticRequestConfig(data.ResticConfig); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	s.SetRestoring(true)
	hasError := true
	defer func() {
		if !hasError {
			return
		}

		s.SetRestoring(false)
	}()

	logger.Info("processing server backup restore request")
	if data.TruncateDirectory {
		logger.Info("received \"truncate_directory\" flag in request: deleting server files")
		if err := s.Filesystem().TruncateRootDirectory(); err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
	}

	// Now that we've cleaned up the data directory if necessary, grab the backup file
	// and attempt to restore it into the server directory.
	if data.Adapter == backup.LocalBackupAdapter {
		b, _, err := backup.LocateLocal(client, c.Param("backup"), s.ID())
		if err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
		go func(s *server.Server, b backup.BackupInterface, logger *log.Entry) {
			logger.Info("starting restoration process for server backup using local driver")
			if err := s.RestoreBackup(b, nil); err != nil {
				logger.WithField("error", err).Error("failed to restore local backup to server")
			}
			s.Events().Publish(server.DaemonMessageEvent, "Completed server restoration from local backup.")
			s.Events().Publish(server.BackupRestoreCompletedEvent, b.Identifier())
			logger.Info("completed server restoration from local backup")
			s.SetRestoring(false)
		}(s, b, logger)
		hasError = false
		c.Status(http.StatusAccepted)
		return
	}

	// Handle restic backup restoration - the backup is stored in the restic repository,
	// no download URL is needed as restic pulls directly from the S3 repo.
	if data.Adapter == backup.ResticBackupAdapter {
		b := backup.NewRestic(client, c.Param("backup"), s.ID(), "", data.ResticConfig)
		b.WithLogContext(map[string]interface{}{
			"server":     s.ID(),
			"request_id": c.GetString("request_id"),
		})
		go func(s *server.Server, b backup.BackupInterface, logger *log.Entry) {
			logger.Info("starting restoration process for server backup using restic driver")
			if err := s.RestoreBackup(b, nil); err != nil {
				logger.WithField("error", errors.WithStack(err)).Error("failed to restore restic backup to server")
			}
			s.Events().Publish(server.DaemonMessageEvent, "Completed server restoration from restic backup.")
			s.Events().Publish(server.BackupRestoreCompletedEvent, b.Identifier())
			logger.Info("completed server restoration from restic backup")
			s.SetRestoring(false)
		}(s, b, logger)
		hasError = false
		c.Status(http.StatusAccepted)
		return
	}

	// Since this is not a local or restic backup we need to stream the archive and then
	// parse over the contents as we go in order to restore it to the server.
	httpClient := http.Client{}
	logger.Info("downloading backup from remote location...")
	// TODO: this will hang if there is an issue. We can't use c.Request.Context() (or really any)
	//  since it will be canceled when the request is closed which happens quickly since we push
	//  this into the background.
	//
	// For now I'm just using the server context so at least the request is canceled if
	// the server gets deleted.
	req, err := http.NewRequestWithContext(s.Context(), http.MethodGet, data.DownloadUrl, nil)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}
	res, err := httpClient.Do(req)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}
	// Don't allow content types that we know are going to give us problems.
	if res.Header.Get("Content-Type") == "" || !strings.Contains("application/x-gzip application/gzip", res.Header.Get("Content-Type")) {
		_ = res.Body.Close()
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "The provided backup link is not a supported content type. \"" + res.Header.Get("Content-Type") + "\" is not application/x-gzip.",
		})
		return
	}

	go func(s *server.Server, uuid string, logger *log.Entry) {
		logger.Info("starting restoration process for server backup using S3 driver")
		if err := s.RestoreBackup(backup.NewS3(client, uuid, s.ID(), ""), res.Body); err != nil {
			logger.WithField("error", errors.WithStack(err)).Error("failed to restore remote S3 backup to server")
		}
		s.Events().Publish(server.DaemonMessageEvent, "Completed server restoration from S3 backup.")
		s.Events().Publish(server.BackupRestoreCompletedEvent, uuid)
		logger.Info("completed server restoration from S3 backup")
		s.SetRestoring(false)
	}(s, c.Param("backup"), logger)

	hasError = false
	c.Status(http.StatusAccepted)
}

// getServerBackupSnapshots lists all restic snapshots for a server.
func getServerBackupSnapshots(c *gin.Context) {
	serverID := c.Param("server")
	client := middleware.ExtractApiClient(c)

	if !config.Get().System.Backups.Restic.Enabled {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "The restic backup adapter is not enabled on this node.",
		})
		return
	}

	// Check if client wants size information (default: false for performance)
	includeStats := c.Query("include_stats") == "true"
	var data resticQueryBody
	if c.Request.Method == http.MethodGet {
		abortLegacyResticGet(c)
		return
	}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateResticRequestConfig(data.ResticConfig); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	includeStats = data.IncludeStats

	b := backup.NewRestic(client, "", serverID, "", data.ResticConfig)
	b.WithLogContext(map[string]interface{}{
		"server":     serverID,
		"request_id": c.GetString("request_id"),
	})

	snapshots, err := b.ListSnapshots(c.Request.Context(), includeStats)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshots": snapshots,
	})
}

// getServerBackupStatus checks if a specific backup snapshot exists in the restic repository.
func getServerBackupStatus(c *gin.Context) {
	serverID := c.Param("server")
	client := middleware.ExtractApiClient(c)

	if !config.Get().System.Backups.Restic.Enabled {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "The restic backup adapter is not enabled on this node.",
		})
		return
	}

	includeStats := c.Query("include_stats") == "true"
	var data resticQueryBody
	if c.Request.Method == http.MethodGet {
		abortLegacyResticGet(c)
		return
	}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateResticRequestConfig(data.ResticConfig); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	includeStats = data.IncludeStats

	b := backup.NewRestic(client, c.Param("backup"), serverID, "", data.ResticConfig)
	b.WithLogContext(map[string]interface{}{
		"server":     serverID,
		"request_id": c.GetString("request_id"),
	})

	snapshot, err := b.GetSnapshotStatus(c.Request.Context(), includeStats)
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"exists":   snapshot != nil,
		"snapshot": snapshot,
	})
}

// deleteServerBackup deletes a backup of a server. For local backups, if the backup
// is not found on the machine just return a 404 error. The service calling this
// endpoint can make its own decisions as to how it wants to handle that response.
// For restic backups, this removes the snapshot from the restic repository.
func deleteServerBackup(c *gin.Context) {
	serverID := c.Param("server")
	client := middleware.ExtractApiClient(c)

	// Parse optional request body to determine adapter type
	// Default to local (wings) adapter for backward compatibility
	var data struct {
		Adapter      backup.AdapterType          `json:"adapter"`
		ResticConfig *backup.ResticRuntimeConfig `json:"restic_config"`
	}
	// Attempt to parse the body, but don't fail if empty (backward compatibility)
	_ = c.ShouldBindJSON(&data)

	// Default to local adapter if not specified
	if data.Adapter == "" {
		data.Adapter = backup.LocalBackupAdapter
	}

	// Handle restic backup deletion
	if data.Adapter == backup.ResticBackupAdapter {
		if !config.Get().System.Backups.Restic.Enabled {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "The restic backup adapter is not enabled on this node.",
			})
			return
		}
		if err := validateResticRequestConfig(data.ResticConfig); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		b := backup.NewRestic(client, c.Param("backup"), serverID, "", data.ResticConfig)
		b.WithLogContext(map[string]interface{}{
			"server":     serverID,
			"request_id": c.GetString("request_id"),
		})
		if err := b.Remove(); err != nil {
			middleware.CaptureAndAbort(c, err)
			return
		}
		c.Status(http.StatusNoContent)
		return
	}

	// Handle local backup deletion (default behavior)
	manager := middleware.ExtractManager(c)
	if _, ok := manager.Get(serverID); !ok {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "The requested resource does not exist on this instance.",
		})
		return
	}

	b, _, err := backup.LocateLocal(client, c.Param("backup"), serverID)
	if err != nil {
		// Just return from the function at this point if the backup was not located.
		if errors.Is(err, os.ErrNotExist) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "The requested backup was not found on this server.",
			})
			return
		}
		middleware.CaptureAndAbort(c, err)
		return
	}
	// I'm not entirely sure how likely this is to happen, however if we did manage to
	// locate the backup previously and it is now missing when we go to delete, just
	// treat it as having been successful, rather than returning a 404.
	if err := b.Remove(); err != nil && !errors.Is(err, os.ErrNotExist) {
		middleware.CaptureAndAbort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
