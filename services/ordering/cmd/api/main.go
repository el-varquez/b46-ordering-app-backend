package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	identityoauth "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/oauth"
	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	identityapp "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	identitytransport "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/transport"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/config"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/httpserver"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/observability"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ordering API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	processConfig, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, processConfig.Environment, processConfig.LogLevel)
	slog.SetDefault(logger)

	processContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	database, err := postgres.Open(processContext, processConfig.DatabaseURL, processConfig.DatabaseHealthTimeout)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := database.Check(processContext); err != nil {
		return errors.New("database is not ready; apply required migrations before starting the API")
	}

	passwordHasher := identitysecurity.NewArgon2id(
		processConfig.ArgonMemoryKiB,
		processConfig.ArgonIterations,
		processConfig.ArgonParallelism,
	)
	providerHTTPClient := &http.Client{Timeout: 5 * time.Second}
	oauthVerifier := identityoauth.New(
		processContext,
		providerHTTPClient,
		processConfig.GoogleClientIDs,
		processConfig.AppleClientIDs,
	)
	identityService, err := identityapp.New(
		identitypostgres.New(database.Pool()),
		passwordHasher,
		identitysecurity.RandomTokenGenerator{},
		identitysecurity.SystemClock{},
		oauthVerifier,
		identityapp.Config{
			AccessTokenLifetime:  processConfig.AccessTokenLifetime,
			RefreshTokenLifetime: processConfig.RefreshTokenLifetime,
			OAuthIntentLifetime:  processConfig.OAuthIntentLifetime,
		},
	)
	if err != nil {
		return err
	}
	identityRoutes := identitytransport.New(identityService, httpserver.JSONResponder{})

	server := httpserver.New(httpserver.Options{
		Address:             processConfig.HTTPAddress,
		ReadTimeout:         processConfig.HTTPReadTimeout,
		WriteTimeout:        processConfig.HTTPWriteTimeout,
		IdleTimeout:         processConfig.HTTPIdleTimeout,
		MaxRequestBodyBytes: processConfig.MaxRequestBodyBytes,
		Logger:              logger,
		Readiness:           database,
		Routes:              []httpserver.RouteRegistrar{identityRoutes},
	})

	serverError := make(chan error, 1)
	go func() {
		logger.Info("ordering API started", "address", processConfig.HTTPAddress, "environment", processConfig.Environment)
		serverError <- server.ListenAndServe()
	}()

	select {
	case <-processContext.Done():
		logger.Info("ordering API shutdown requested")
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), processConfig.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}
	logger.Info("ordering API stopped cleanly")
	return nil
}
