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
	defaultHTTPPort             = "8080"
	defaultMaxRequestBodyBytes  = int64(1 << 20)
	minimumMaxRequestBodyBytes  = int64(1024)
	maximumMaxRequestBodyBytes  = int64(10 << 20)
	minimumOperationalTimeout   = 100 * time.Millisecond
	maximumOperationalTimeout   = 5 * time.Minute
	defaultAccessTokenLifetime  = 15 * time.Minute
	defaultRefreshTokenLifetime = 30 * 24 * time.Hour
	defaultOAuthIntentLifetime  = 10 * time.Minute
	defaultArgonMemoryKiB       = int64(19 * 1024)
	defaultArgonIterations      = int64(2)
	defaultArgonParallelism     = int64(1)
	defaultOutboxPollInterval   = 500 * time.Millisecond
	defaultOutboxBatchSize      = int64(10)
	defaultOutboxLeaseTimeout   = 30 * time.Second
	defaultOutboxRetryBase      = time.Second
	defaultOutboxRetryMax       = time.Minute
	defaultInventoryTimeout     = 5 * time.Second
	defaultInventoryBodyBytes   = int64(256 * 1024)
)

// Config is the validated process configuration. It is created once at startup
// and passed by value through the composition root.
type Config struct {
	Environment                   string
	HTTPAddress                   string
	DatabaseURL                   string
	LogLevel                      slog.Level
	HTTPReadTimeout               time.Duration
	HTTPWriteTimeout              time.Duration
	HTTPIdleTimeout               time.Duration
	ShutdownTimeout               time.Duration
	DatabaseHealthTimeout         time.Duration
	MaxRequestBodyBytes           int64
	AccessTokenLifetime           time.Duration
	RefreshTokenLifetime          time.Duration
	OAuthIntentLifetime           time.Duration
	ArgonMemoryKiB                uint32
	ArgonIterations               uint32
	ArgonParallelism              uint8
	GoogleClientIDs               []string
	AppleClientIDs                []string
	OutboxPollInterval            time.Duration
	OutboxBatchSize               int
	OutboxLeaseTimeout            time.Duration
	OutboxRetryBase               time.Duration
	OutboxRetryMax                time.Duration
	InventoryAdapterMode          string
	InventoryAdapterURL           string
	InventoryAdapterToken         string
	InventoryAdapterTimeout       time.Duration
	InventoryMaxResponseBodyBytes int64
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

	accessLifetime, err := identityDuration("ACCESS_TOKEN_LIFETIME", defaultAccessTokenLifetime)
	if err != nil {
		return Config{}, err
	}
	refreshLifetime, err := longDuration("REFRESH_TOKEN_LIFETIME", defaultRefreshTokenLifetime)
	if err != nil {
		return Config{}, err
	}
	oauthIntentLifetime, err := identityDuration("OAUTH_INTENT_LIFETIME", defaultOAuthIntentLifetime)
	if err != nil {
		return Config{}, err
	}
	if refreshLifetime <= accessLifetime {
		return Config{}, errors.New("REFRESH_TOKEN_LIFETIME must be longer than ACCESS_TOKEN_LIFETIME")
	}

	argonMemory, err := integer("ARGON2_MEMORY_KIB", defaultArgonMemoryKiB)
	if err != nil || argonMemory < 19*1024 || argonMemory > 1024*1024 {
		return Config{}, errors.New("ARGON2_MEMORY_KIB must be from 19456 to 1048576")
	}
	argonIterations, err := integer("ARGON2_ITERATIONS", defaultArgonIterations)
	if err != nil || argonIterations < 2 || argonIterations > 20 {
		return Config{}, errors.New("ARGON2_ITERATIONS must be from 2 to 20")
	}
	argonParallelism, err := integer("ARGON2_PARALLELISM", defaultArgonParallelism)
	if err != nil || argonParallelism < 1 || argonParallelism > 16 {
		return Config{}, errors.New("ARGON2_PARALLELISM must be from 1 to 16")
	}

	googleClientIDs := commaSeparated("GOOGLE_CLIENT_IDS")
	appleClientIDs := commaSeparated("APPLE_CLIENT_IDS")
	if environment == "production" && (len(googleClientIDs) == 0 || len(appleClientIDs) == 0) {
		return Config{}, errors.New("GOOGLE_CLIENT_IDS and APPLE_CLIENT_IDS are required in production")
	}

	outboxPollInterval, err := boundedDuration(
		"OUTBOX_POLL_INTERVAL", defaultOutboxPollInterval, 50*time.Millisecond, time.Minute,
	)
	if err != nil {
		return Config{}, err
	}
	outboxBatchSize, err := integer("OUTBOX_BATCH_SIZE", defaultOutboxBatchSize)
	if err != nil || outboxBatchSize < 1 || outboxBatchSize > 100 {
		return Config{}, errors.New("OUTBOX_BATCH_SIZE must be from 1 to 100")
	}
	outboxLeaseTimeout, err := boundedDuration(
		"OUTBOX_LEASE_TIMEOUT", defaultOutboxLeaseTimeout, 100*time.Millisecond, 10*time.Minute,
	)
	if err != nil {
		return Config{}, err
	}
	if outboxLeaseTimeout <= outboxPollInterval {
		return Config{}, errors.New("OUTBOX_LEASE_TIMEOUT must be longer than OUTBOX_POLL_INTERVAL")
	}
	outboxRetryBase, err := boundedDuration(
		"OUTBOX_RETRY_BASE", defaultOutboxRetryBase, 100*time.Millisecond, 10*time.Minute,
	)
	if err != nil {
		return Config{}, err
	}
	outboxRetryMax, err := boundedDuration(
		"OUTBOX_RETRY_MAX", defaultOutboxRetryMax, 100*time.Millisecond, time.Hour,
	)
	if err != nil {
		return Config{}, err
	}
	if outboxRetryMax < outboxRetryBase {
		return Config{}, errors.New("OUTBOX_RETRY_MAX must not be shorter than OUTBOX_RETRY_BASE")
	}

	inventoryMode := strings.ToUpper(envOrDefault("INVENTORY_ADAPTER_MODE", "FAKE"))
	if inventoryMode != "FAKE" && inventoryMode != "HTTP" {
		return Config{}, errors.New("INVENTORY_ADAPTER_MODE must be FAKE or HTTP")
	}
	if environment == "production" && inventoryMode != "HTTP" {
		return Config{}, errors.New("INVENTORY_ADAPTER_MODE must be HTTP in production")
	}
	inventoryURL := strings.TrimSpace(os.Getenv("INVENTORY_ADAPTER_URL"))
	inventoryToken := os.Getenv("INVENTORY_ADAPTER_TOKEN")
	if inventoryMode == "HTTP" {
		parsedURL, parseErr := url.Parse(inventoryURL)
		if parseErr != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return Config{}, errors.New("INVENTORY_ADAPTER_URL must be an absolute HTTP(S) URL in HTTP mode")
		}
		if environment == "production" && parsedURL.Scheme != "https" {
			return Config{}, errors.New("INVENTORY_ADAPTER_URL must use HTTPS in production")
		}
		if len([]byte(inventoryToken)) < 32 {
			return Config{}, errors.New("INVENTORY_ADAPTER_TOKEN must contain at least 32 bytes in HTTP mode")
		}
	}
	inventoryTimeout, err := boundedDuration(
		"INVENTORY_ADAPTER_TIMEOUT", defaultInventoryTimeout, 100*time.Millisecond, outboxLeaseTimeout,
	)
	if err != nil {
		return Config{}, err
	}
	inventoryBodyBytes, err := integer("INVENTORY_MAX_RESPONSE_BODY_BYTES", defaultInventoryBodyBytes)
	if err != nil || inventoryBodyBytes < 1024 || inventoryBodyBytes > 1024*1024 {
		return Config{}, errors.New("INVENTORY_MAX_RESPONSE_BODY_BYTES must be from 1024 to 1048576")
	}

	return Config{
		Environment:                   environment,
		HTTPAddress:                   net.JoinHostPort(host, port),
		DatabaseURL:                   databaseURL,
		LogLevel:                      logLevel,
		HTTPReadTimeout:               readTimeout,
		HTTPWriteTimeout:              writeTimeout,
		HTTPIdleTimeout:               idleTimeout,
		ShutdownTimeout:               shutdownTimeout,
		DatabaseHealthTimeout:         databaseHealthTimeout,
		MaxRequestBodyBytes:           maxBodyBytes,
		AccessTokenLifetime:           accessLifetime,
		RefreshTokenLifetime:          refreshLifetime,
		OAuthIntentLifetime:           oauthIntentLifetime,
		ArgonMemoryKiB:                uint32(argonMemory),
		ArgonIterations:               uint32(argonIterations),
		ArgonParallelism:              uint8(argonParallelism),
		GoogleClientIDs:               googleClientIDs,
		AppleClientIDs:                appleClientIDs,
		OutboxPollInterval:            outboxPollInterval,
		OutboxBatchSize:               int(outboxBatchSize),
		OutboxLeaseTimeout:            outboxLeaseTimeout,
		OutboxRetryBase:               outboxRetryBase,
		OutboxRetryMax:                outboxRetryMax,
		InventoryAdapterMode:          inventoryMode,
		InventoryAdapterURL:           inventoryURL,
		InventoryAdapterToken:         inventoryToken,
		InventoryAdapterTimeout:       inventoryTimeout,
		InventoryMaxResponseBodyBytes: inventoryBodyBytes,
	}, nil
}

func boundedDuration(key string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := envOrDefault(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be a duration from %s to %s", key, minimum, maximum)
	}
	return parsed, nil
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

func identityDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := envOrDefault(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Minute || parsed > 24*time.Hour {
		return 0, fmt.Errorf("%s must be a duration from 1m to 24h", key)
	}
	return parsed, nil
}

func longDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := envOrDefault(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Hour || parsed > 365*24*time.Hour {
		return 0, fmt.Errorf("%s must be a duration from 1h to 8760h", key)
	}
	return parsed, nil
}

func commaSeparated(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, candidate := range strings.Split(raw, ",") {
		value := strings.TrimSpace(candidate)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
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
