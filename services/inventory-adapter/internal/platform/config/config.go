package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	minimumSecretBytes = 32
	minimumBodyBytes   = int64(1024)
	maximumBodyBytes   = int64(1024 * 1024)
)

type Config struct {
	Environment           string
	HTTPAddress           string
	StoreDatabaseURL      string
	ServiceTokenDigest    [32]byte
	MovementActorID       uuid.UUID
	LogLevel              slog.Level
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	IdleTimeout           time.Duration
	ShutdownTimeout       time.Duration
	DatabaseHealthTimeout time.Duration
	MaxRequestBodyBytes   int64
}

func Load() (Config, error) {
	environment := strings.ToLower(envOrDefault("APP_ENV", "development"))
	switch environment {
	case "development", "test", "staging", "production":
	default:
		return Config{}, errors.New("APP_ENV must be development, test, staging, or production")
	}

	databaseURL := strings.TrimSpace(os.Getenv("STORE_DATABASE_URL"))
	parsedURL, err := url.Parse(databaseURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql") {
		return Config{}, errors.New("STORE_DATABASE_URL must be a valid postgres URL")
	}

	serviceToken := os.Getenv("INVENTORY_SERVICE_TOKEN")
	if len([]byte(serviceToken)) < minimumSecretBytes {
		return Config{}, errors.New("INVENTORY_SERVICE_TOKEN must contain at least 32 bytes")
	}
	tokenDigest := sha256.Sum256([]byte(serviceToken))

	movementActorID, err := uuid.Parse(strings.TrimSpace(os.Getenv("POS_MOVEMENT_ACTOR_ID")))
	if err != nil || movementActorID == uuid.Nil {
		return Config{}, errors.New("POS_MOVEMENT_ACTOR_ID must be a non-zero UUID")
	}

	port := envOrDefault("HTTP_PORT", "8081")
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, errors.New("HTTP_PORT must be from 1 to 65535")
	}

	logLevel, err := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := boundedDuration("HTTP_READ_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := boundedDuration("HTTP_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := boundedDuration("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := boundedDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	healthTimeout, err := boundedDuration("DATABASE_HEALTH_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}

	maxBody, err := strconv.ParseInt(envOrDefault("MAX_REQUEST_BODY_BYTES", "262144"), 10, 64)
	if err != nil || maxBody < minimumBodyBytes || maxBody > maximumBodyBytes {
		return Config{}, fmt.Errorf("MAX_REQUEST_BODY_BYTES must be from %d to %d", minimumBodyBytes, maximumBodyBytes)
	}

	return Config{
		Environment: environment, HTTPAddress: net.JoinHostPort(envOrDefault("HTTP_HOST", "0.0.0.0"), port),
		StoreDatabaseURL: databaseURL, ServiceTokenDigest: tokenDigest, MovementActorID: movementActorID,
		LogLevel: logLevel, ReadTimeout: readTimeout, WriteTimeout: writeTimeout,
		IdleTimeout: idleTimeout, ShutdownTimeout: shutdownTimeout,
		DatabaseHealthTimeout: healthTimeout, MaxRequestBodyBytes: maxBody,
	}, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func boundedDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := envOrDefault(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 100*time.Millisecond || parsed > 5*time.Minute {
		return 0, fmt.Errorf("%s must be from 100ms to 5m", key)
	}
	return parsed, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(value))); err != nil {
		return 0, errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	return level, nil
}
