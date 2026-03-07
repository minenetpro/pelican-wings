package router

import (
	"context"
	"net/http"
	"time"

	"emperror.dev/errors"
	"github.com/Minenetpro/pelican-wings/environment"
	"github.com/Minenetpro/pelican-wings/remote"
	"github.com/Minenetpro/pelican-wings/router/middleware"
	"github.com/Minenetpro/pelican-wings/server"
	"github.com/Minenetpro/pelican-wings/server/installer"
	"github.com/gin-gonic/gin"
	"github.com/goccy/go-json"
)

type serverControlRequest struct {
	UUID                 string                       `json:"uuid"`
	StartOnCompletion    bool                         `json:"start_on_completion"`
	SkipInstall          bool                         `json:"skip_install"`
	Settings             json.RawMessage              `json:"settings"`
	ProcessConfiguration *remote.ProcessConfiguration `json:"process_configuration"`
	InstallationScript   *remote.InstallationScript   `json:"installation_script"`
}

func extractLocalStore(c *gin.Context) (remote.LocalServerStore, bool) {
	client := middleware.ExtractApiClient(c)
	store, ok := client.(remote.LocalServerStore)
	return store, ok
}

func definitionFromRequest(request serverControlRequest) (remote.LocalServerDefinition, error) {
	if request.UUID == "" {
		return remote.LocalServerDefinition{}, errors.New("missing server uuid")
	}
	if len(request.Settings) == 0 {
		return remote.LocalServerDefinition{}, errors.New("missing server settings payload")
	}
	if request.ProcessConfiguration == nil {
		return remote.LocalServerDefinition{}, errors.New("missing process configuration payload")
	}

	return remote.LocalServerDefinition{
		UUID: request.UUID,
		Configuration: remote.ServerConfigurationResponse{
			Settings:             append([]byte(nil), request.Settings...),
			ProcessConfiguration: request.ProcessConfiguration,
		},
		InstallationScript: request.InstallationScript,
	}, nil
}

func restoreImportedServerState(s *server.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	running, err := s.Environment.IsRunning(ctx)
	if err != nil {
		s.Log().WithField("error", err).Warn("failed to inspect imported server state")
		return
	}

	if !running {
		s.Environment.SetState(environment.ProcessOfflineState)
		return
	}

	s.Environment.SetState(environment.ProcessRunningState)
	if err := s.Environment.Attach(ctx); err != nil {
		s.Log().WithField("error", err).Warn("failed to attach to imported running server")
	}
}

func putServer(c *gin.Context) {
	store, ok := extractLocalStore(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
			"error": "The configured control plane does not support local server updates.",
		})
		return
	}

	request := serverControlRequest{}
	if err := c.BindJSON(&request); err != nil {
		return
	}
	if request.UUID == "" {
		request.UUID = c.Param("server")
	}
	if request.UUID != c.Param("server") {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "The request uuid does not match the target server.",
		})
		return
	}

	definition, err := definitionFromRequest(request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": err.Error(),
		})
		return
	}
	if definition.InstallationScript == nil {
		if existing, err := store.GetLocalServer(c.Request.Context(), request.UUID); err == nil {
			definition.InstallationScript = existing.InstallationScript
		}
	}

	if err := store.UpsertServer(c.Request.Context(), definition); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	s := middleware.ExtractServer(c)
	if err := s.Sync(); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	c.JSON(http.StatusOK, s.ToAPIResponse())
}

func createLocalServer(c *gin.Context, manager *server.Manager, request serverControlRequest) {
	store, ok := extractLocalStore(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
			"error": "The configured control plane does not support local server provisioning.",
		})
		return
	}

	definition, err := definitionFromRequest(request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": err.Error(),
		})
		return
	}
	if _, exists := manager.Get(request.UUID); exists {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "The requested server already exists on this instance.",
		})
		return
	}

	if err := store.UpsertServer(c.Request.Context(), definition); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	install, err := installer.New(c.Request.Context(), manager, installer.ServerDetails{
		UUID:              request.UUID,
		StartOnCompletion: request.StartOnCompletion,
	})
	if err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	manager.Add(install.Server())

	go func(i *installer.Installer, skipInstall bool) {
		if err := i.Server().CreateEnvironment(); err != nil {
			i.Server().Log().WithField("error", err).Error("failed to create server environment during create process")
			return
		}

		if skipInstall {
			restoreImportedServerState(i.Server())
			if i.StartOnCompletion {
				if err := i.Server().HandlePowerAction(server.PowerActionStart, 30); err != nil {
					i.Server().Log().WithField("error", err).Warn("failed to start imported server after create")
				}
			}
			return
		}

		if err := i.Server().Install(); err != nil {
			i.Server().Log().WithField("error", err).Error("failed to run install process for server")
			return
		}

		if i.StartOnCompletion {
			if err := i.Server().HandlePowerAction(server.PowerActionStart, 30); err != nil {
				i.Server().Log().WithField("error", err).Warn("failed to start server after installation")
			}
		}
	}(install, request.SkipInstall)

	c.Status(http.StatusAccepted)
}
