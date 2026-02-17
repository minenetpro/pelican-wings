package router

import (
	"emperror.dev/errors"
	"github.com/Minenetpro/pelican-wings/config"
	"github.com/Minenetpro/pelican-wings/remote"
	"github.com/Minenetpro/pelican-wings/router/middleware"
	wserver "github.com/Minenetpro/pelican-wings/server"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"
)

// Configure configures the routing infrastructure for this daemon instance.
func Configure(m *wserver.Manager, client remote.Client) *gin.Engine {
	gin.SetMode("release")

	router := gin.New()
	router.Use(gin.Recovery())
	if err := router.SetTrustedProxies(config.Get().Api.TrustedProxies); err != nil {
		panic(errors.WithStack(err))
		return nil
	}
	router.Use(middleware.AttachRequestID(), middleware.CaptureErrors(), middleware.SetAccessControlHeaders())
	router.Use(middleware.AttachServerManager(m), middleware.AttachApiClient(client))
	// @todo log this into a different file so you can setup IP blocking for abusive requests and such.
	// This should still dump requests in debug mode since it does help with understanding the request
	// lifecycle and quickly seeing what was called leading to the logs. However, it isn't feasible to mix
	// this output in production and still get meaningful logs from it since they'll likely just be a huge
	// spamfest.
	router.Use(gin.LoggerWithFormatter(func(params gin.LogFormatterParams) string {
		log.WithFields(log.Fields{
			"client_ip":  params.ClientIP,
			"status":     params.StatusCode,
			"latency":    params.Latency,
			"request_id": params.Keys["request_id"],
		}).Debugf("%s %s", params.MethodColor()+params.Method+params.ResetColor(), params.Path)

		return ""
	}))

	// These routes use signed URLs to validate access to the resource being requested.
	router.GET("/download/backup", getDownloadBackup)
	router.GET("/download/file", getDownloadFile)
	router.POST("/upload/file", postServerUploadFiles)

	// This route is special it sits above all the other requests because we are
	// using a JWT to authorize access to it, therefore it needs to be publicly
	// accessible.
	router.GET("/api/servers/:server/ws", middleware.ServerExists(), getServerWebsocket)

	// This request is called by another daemon when a server is going to be transferred out.
	// This request does not need the AuthorizationMiddleware as the panel should never call it
	// and requests are authenticated through a JWT the panel issues to the other daemon.
	router.POST("/api/transfers", postTransfers)

	// All the routes beyond this mount will use an authorization middleware
	// and will not be accessible without the correct Authorization header provided.
	protected := router.Use(middleware.RequireAuthorization())
	protected.POST("/api/update", postUpdateConfiguration)
	protected.GET("/api/system", getSystemInformation)
	protected.GET("/api/diagnostics", getDiagnostics)
	protected.GET("/api/system/docker/disk", getDockerDiskUsage)
	protected.DELETE("/api/system/docker/image/prune", pruneDockerImages)
	protected.GET("/api/system/ips", getSystemIps)
	protected.GET("/api/system/utilization", getSystemUtilization)
	protected.GET("/api/servers", getAllServers)
	protected.POST("/api/servers", postCreateServer)
	protected.DELETE("/api/transfers/:server", deleteTransfer)
	protected.POST("/api/deauthorize-user", postDeauthorizeUser)
	protected.GET("/api/events", getServerEvents)

	// These are server specific routes, and require that the request be authorized.
	server := router.Group("/api/servers/:server")
	server.Use(middleware.RequireAuthorization())
	{
		backup := server.Group("/backup")
		{
			backup.GET("/snapshots", getServerBackupSnapshots)
			backup.GET("/:backup/status", getServerBackupStatus)
			backup.DELETE("/:backup", deleteServerBackup)
		}

		serverExisting := server.Group("")
		serverExisting.Use(middleware.ServerExists())
		{
			serverExisting.GET("", getServer)
			serverExisting.DELETE("", deleteServer)

			serverExisting.GET("/logs", getServerLogs)
			serverExisting.GET("/console", getServerConsole)
			serverExisting.GET("/install-logs", getServerInstallLogs)
			serverExisting.GET("/install-status", getServerInstallStatus)
			serverExisting.POST("/power", postServerPower)
			serverExisting.POST("/commands", postServerCommands)
			serverExisting.POST("/install", postServerInstall)
			serverExisting.POST("/reinstall", postServerReinstall)
			serverExisting.POST("/sync", postServerSync)
			serverExisting.POST("/ws/deny", postServerDenyWSTokens)

			// This archive request causes the archive to start being created
			// this should only be triggered by the panel.
			serverExisting.POST("/transfer", postServerTransfer)
			serverExisting.DELETE("/transfer", deleteServerTransfer)

			// Deletes all backups for a server
			serverExisting.DELETE("deleteAllBackups", deleteAllServerBackups)

			files := serverExisting.Group("/files")
			{
				files.GET("/contents", getServerFileContents)
				files.GET("/list-directory", getServerListDirectory)
				files.PUT("/rename", putServerRenameFiles)
				files.POST("/copy", postServerCopyFile)
				files.POST("/write", postServerWriteFile)
				files.POST("/create-directory", postServerCreateDirectory)
				files.POST("/delete", postServerDeleteFiles)
				files.POST("/compress", postServerCompressFiles)
				files.POST("/decompress", postServerDecompressFiles)
				files.POST("/chmod", postServerChmodFile)
				files.GET("/search", getFilesBySearch)

				files.GET("/pull", middleware.RemoteDownloadEnabled(), getServerPullingFiles)
				files.POST("/pull", middleware.RemoteDownloadEnabled(), postServerPullRemoteFile)
				files.DELETE("/pull/:download", middleware.RemoteDownloadEnabled(), deleteServerPullRemoteFile)
			}

			backupExisting := serverExisting.Group("/backup")
			{
				backupExisting.POST("", postServerBackup)
				backupExisting.POST("/:backup/restore", postServerRestoreBackup)
			}
		}
	}

	return router
}
