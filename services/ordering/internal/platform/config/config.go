package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPPort            = "8080"
	defaultMaxRequestBodyBytes = int64(1 << 20)
	minimumMaxRequestBodyBytes = int64(1024)
	maximumMaxRequestBodyBytes = int64(10 << 20)
	minimumOperationalTimeout  = 100 * time.Millisecond
	maximumOperationalTimeout  = 5 * time.Minute
)

// Config is the validated process configuration. It is created once at startup
// and passed by value through the composition root.
type Config struct {
	Environment           string
	HTTPAddress           string
	DatabaseURL           string
	LogLevel              slog.Level
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	ShutdownTimeout       time.Duration
	DatabaseHealthTimeout time.Duration
	MaxRequestBodyBytes   int64
}

// Load reads environment variables and rejects unsafe or malformed startup
// configuration before the process opens a network listener.
func Load() (Config, error) {
	environment, err := parseEnvironment(envOrDefault("APP_ENV", "development"))
	if err != nil {
		return Config{}, err
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	parsedDatabaseURL, err := url.Parse(databaseURL)
	if err != nil || (parsedDatabaseURL.Scheme != "postgres" && parsedDatabaseURL.Scheme != "postgresql") || parsedDatabaseURL.Host == "" {
		return Config{}, errors.New("DATABASE_URL must be a valid postgres URL")
	}

	host := envOrDefault("HTTP_HOST", "0.0.0.0")
	port := envOrDefault("HTTP_PORT", defaultHTTPPort)
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, errors.New("HTTP_PORT must be an integer from 1 to 65535")
	}

	logLevel, err := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	readTimeout, err := duration("HTTP_READ_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := duration("HTTP_WRITE_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := duration("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := duration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	databaseHealthTimeout, err := duration("DATABASE_HEALTH_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}

	maxBodyBytes, err := integer("MAX_REQUEST_BODY_BYTES", defaultMaxRequestBodyBytes)
	if err != nil || maxBodyBytes < minimumMaxRequestBodyBytes || maxBodyBytes > maximumMaxRequestBodyBytes {
		return Config{}, fmt.Errorf("MAX_REQUEST_BODY_BYTES must be an integer from %d to %d", minimumMaxRequestBodyBytes, maximumMaxRequestBodyBytes)
	}

	return Config{
		Environment:           environment,
		HTTPAddress:           net.JoinHostPort(host, port),
		DatabaseURL:           databaseURL,
		LogLevel:              logLevel,
		HTTPReadTimeout:       readTimeout,
		HTTPWriteTimeout:      writeTimeout,
		HTTPIdleTimeout:       idleTimeout,
		ShutdownTimeout:       shutdownTimeout,
		DatabaseHealthTimeout: databaseHealthTimeout,
		MaxRequestBodyBytes:   maxBodyBytes,
	}, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	value := envOrDefault(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimumOperationalTimeout || parsed > maximumOperationalTimeout {
		return 0, fmt.Errorf("%s must be a duration from %s to %s", key, minimumOperationalTimeout, maximumOperationalTimeout)
	}
	return parsed, nil
}

func integer(key string, fallback int64) (int64, error) {
	value := envOrDefault(key, strconv.FormatInt(fallback, 10))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return parsed, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	value = strings.ToLower(value)
	switch value {
	case "debug", "info", "warn", "error":
	default:
		return 0, errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	return level, nil
}

func parseEnvironment(value string) (string, error) {
	value = strings.ToLower(value)
	switch value {
	case "development", "test", "staging", "production":
		return value, nil
	default:
		return "", errors.New("APP_ENV must be development, test, staging, or production")
	}
}
