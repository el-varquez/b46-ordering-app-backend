package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	inventorypostgres "github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/adapters/postgres"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/adapters/system"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/transport"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/platform/config"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/platform/httpserver"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		slog.Error("inventory adapter stopped", "error", err)
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

	database, err := inventorypostgres.Open(processContext, processConfig.StoreDatabaseURL,
		processConfig.DatabaseHealthTimeout, processConfig.MovementActorID)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Check(processContext); err != nil {
		return errors.New("store database is not ready; apply adapter migration and POS fixture/provisioning")
	}
	store, err := inventorypostgres.New(database.Pool(), system.IDs{}, system.Clock{}, processConfig.MovementActorID)
	if err != nil {
		return err
	}
	service, err := application.NewService(store)
	if err != nil {
		return err
	}
	routes, err := transport.New(service, processConfig.ServiceTokenDigest, processConfig.MaxRequestBodyBytes, logger)
	if err != nil {
		return err
	}
	server := httpserver.New(httpserver.Options{Address: processConfig.HTTPAddress,
		ReadTimeout: processConfig.ReadTimeout, WriteTimeout: processConfig.WriteTimeout,
		IdleTimeout: processConfig.IdleTimeout, Logger: logger, Readiness: database,
		Routes: []httpserver.RouteRegistrar{routes}})

	serverError := make(chan error, 1)
	go func() {
		logger.Info("inventory adapter started", "address", processConfig.HTTPAddress, "environment", processConfig.Environment)
		serverError <- server.ListenAndServe()
	}()
	select {
	case <-processContext.Done():
		logger.Info("inventory adapter shutdown requested")
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), processConfig.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownContext)
}
