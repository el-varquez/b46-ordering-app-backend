package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	migrationsDirectory := flag.String("dir", "migrations", "directory containing SQL migrations")
	flag.Parse()
	command := "up"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	database, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer database.Close()

	if err := database.PingContext(context.Background()); err != nil {
		return fmt.Errorf("connect to migration database: %w", err)
	}
	if err := goose.RunContext(context.Background(), command, database, *migrationsDirectory); err != nil {
		return fmt.Errorf("run migration command %q: %w", command, err)
	}
	return nil
}
