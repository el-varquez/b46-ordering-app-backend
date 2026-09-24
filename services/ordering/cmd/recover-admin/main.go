package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	identityapp "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/config"
	platformpostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/platform/postgres"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	email := strings.TrimSpace(os.Getenv("B46_RECOVER_ADMIN_EMAIL"))
	password := os.Getenv("B46_RECOVER_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return errors.New("B46_RECOVER_ADMIN_EMAIL and B46_RECOVER_ADMIN_PASSWORD are required")
	}
	processConfig, err := config.Load()
	if err != nil {
		return err
	}
	database, err := platformpostgres.Open(ctx, processConfig.DatabaseURL, processConfig.DatabaseHealthTimeout)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Check(ctx); err != nil {
		return errors.New("database is not ready; apply required migrations first")
	}
	identity, err := identityapp.New(
		identitypostgres.New(database.Pool()),
		identitysecurity.NewArgon2id(
			processConfig.ArgonMemoryKiB,
			processConfig.ArgonIterations,
			processConfig.ArgonParallelism,
		),
		identitysecurity.RandomTokenGenerator{},
		identitysecurity.SystemClock{},
		nil,
		identityapp.Config{
			AccessTokenLifetime:  processConfig.AccessTokenLifetime,
			RefreshTokenLifetime: processConfig.RefreshTokenLifetime,
			OAuthIntentLifetime:  processConfig.OAuthIntentLifetime,
		},
	)
	if err != nil {
		return err
	}
	admin, err := identity.RecoverAdminPassword(ctx, email, password)
	if err != nil {
		return err
	}
	fmt.Printf("Admin password recovered and previous sessions revoked: %s\n", admin.ID)
	return nil
}
