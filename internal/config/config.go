package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the Memory OS
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Memory   MemoryConfig   `yaml:"memory"`
	Database DatabaseConfig `yaml:"database"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port         int           `yaml:"port"`
	GRPCPort     int           `yaml:"grpc_port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	TLSCertFile  string        `yaml:"tls_cert_file"`
	TLSKeyFile   string        `yaml:"tls_key_file"`
	EnableTLS    bool          `yaml:"enable_tls"`
}

// ServiceTenant marks an API key held by a trusted backend that may act for any
// tenant; the tenant then comes from the request.
const ServiceTenant = "*"

// AuthConfig holds authentication configuration
type AuthConfig struct {
	RequireAPIKey bool   `yaml:"require_api_key"`
	RequireJWT    bool   `yaml:"require_jwt"`
	RequireMTLS   bool   `yaml:"require_mtls"`
	JWTSecret     string `yaml:"jwt_secret"`
	// APIKeys maps each accepted API key to the tenant it may act for, or to
	// ServiceTenant for a trusted backend that acts for any tenant.
	APIKeys map[string]string `yaml:"api_keys"`
	// AllowUnauthenticated permits starting with no authentication at all, so any
	// caller can read and write any tenant's memory. For local development only.
	AllowUnauthenticated bool   `yaml:"allow_unauthenticated"`
	ClientCACertFile     string `yaml:"client_ca_cert_file"`
}

// placeholderJWTSecret was the default JWT secret of earlier releases. Tokens signed
// with it can be forged by anyone, so it is refused.
const placeholderJWTSecret = "your-secret-key"

// parseAPIKeys reads MEMORY_OS_API_KEYS ("key=tenant,key2=*") and the single
// MEMORY_OS_API_KEY of earlier releases, which is treated as a service key.
func parseAPIKeys(list, single string) (map[string]string, error) {
	keys := map[string]string{}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, tenant, ok := strings.Cut(entry, "=")
		key, tenant = strings.TrimSpace(key), strings.TrimSpace(tenant)
		if !ok || key == "" || tenant == "" {
			return nil, fmt.Errorf("MEMORY_OS_API_KEYS entry %q must be key=tenant or key=*", entry)
		}
		keys[key] = tenant
	}
	if single = strings.TrimSpace(single); single != "" {
		if _, dup := keys[single]; !dup {
			keys[single] = ServiceTenant
		}
	}
	return keys, nil
}

// MemoryConfig holds memory system configuration
type MemoryConfig struct {
	STM STMConfig `yaml:"stm"`
	MTM MTMConfig `yaml:"mtm"`
	LTM LTMConfig `yaml:"ltm"`
}

// STMConfig holds Short-Term Memory configuration
type STMConfig struct {
	CacheMaxTurns int           `yaml:"cache_max_turns"`
	CacheTTL      time.Duration `yaml:"cache_ttl"`
}

// MTMConfig holds Mid-Term Memory configuration
type MTMConfig struct {
	QualityValidationMode  string  `yaml:"quality_validation_mode"`
	HeatPromotionThreshold float64 `yaml:"heat_promotion_threshold"`
	MaxSegmentsPerSession  int     `yaml:"max_segments_per_session"`
	SegmentMergeThreshold  float64 `yaml:"segment_merge_threshold"`
}

// LTMConfig holds Long-Term Memory configuration
type LTMConfig struct {
	Enabled                bool    `yaml:"enabled"`
	PersonaUpdateThreshold float64 `yaml:"persona_update_threshold"`
	KnowledgeGraphEnabled  bool    `yaml:"knowledge_graph_enabled"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Redis      RedisConfig      `yaml:"redis"`
	MongoDB    MongoDBConfig    `yaml:"mongodb"`
	Milvus     MilvusConfig     `yaml:"milvus"`
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// MongoDBConfig holds MongoDB configuration
type MongoDBConfig struct {
	URI      string `yaml:"uri"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// MilvusConfig holds Milvus configuration
type MilvusConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	config := &Config{
		Server: ServerConfig{
			Port:         getEnvInt("MEMORY_OS_PORT", 8080),
			GRPCPort:     getEnvInt("MEMORY_OS_GRPC_PORT", 9090),
			ReadTimeout:  time.Duration(getEnvInt("MEMORY_OS_READ_TIMEOUT", 30)) * time.Second,
			WriteTimeout: time.Duration(getEnvInt("MEMORY_OS_WRITE_TIMEOUT", 30)) * time.Second,
			TLSCertFile:  getEnv("MEMORY_OS_TLS_CERT_FILE", ""),
			TLSKeyFile:   getEnv("MEMORY_OS_TLS_KEY_FILE", ""),
			EnableTLS:    getEnvBool("MEMORY_OS_ENABLE_TLS", false),
		},
		Auth: AuthConfig{
			RequireAPIKey:        getEnvBool("MEMORY_OS_REQUIRE_API_KEY", false),
			RequireJWT:           getEnvBool("MEMORY_OS_REQUIRE_JWT", false),
			RequireMTLS:          getEnvBool("MEMORY_OS_REQUIRE_MTLS", false),
			JWTSecret:            getEnv("MEMORY_OS_JWT_SECRET", ""),
			AllowUnauthenticated: getEnvBool("MEMORY_OS_ALLOW_UNAUTHENTICATED", false),
			ClientCACertFile:     getEnv("MEMORY_OS_CLIENT_CA_CERT_FILE", ""),
		},
		Memory: MemoryConfig{
			STM: STMConfig{
				CacheMaxTurns: getEnvInt("MEMORY_OS_STM_CACHE_MAX_TURNS", 10),
				CacheTTL:      time.Duration(getEnvInt("MEMORY_OS_STM_CACHE_TTL_HOURS", 2)) * time.Hour,
			},
			MTM: MTMConfig{
				QualityValidationMode:  getEnv("MEMORY_OS_MTM_QUALITY_MODE", "balanced"),
				HeatPromotionThreshold: getEnvFloat("MEMORY_OS_MTM_HEAT_THRESHOLD", 0.8),
				MaxSegmentsPerSession:  getEnvInt("MEMORY_OS_MTM_MAX_SEGMENTS", 50),
				SegmentMergeThreshold:  getEnvFloat("MEMORY_OS_MTM_MERGE_THRESHOLD", 0.85),
			},
			LTM: LTMConfig{
				Enabled:                getEnvBool("MEMORY_OS_LTM_ENABLED", true),
				PersonaUpdateThreshold: getEnvFloat("MEMORY_OS_LTM_PERSONA_THRESHOLD", 0.7),
				KnowledgeGraphEnabled:  getEnvBool("MEMORY_OS_LTM_KG_ENABLED", true),
			},
		},
		Database: DatabaseConfig{
			Redis: RedisConfig{
				Host:     getEnv("MEMORY_OS_REDIS_HOST", "localhost"),
				Port:     getEnvInt("MEMORY_OS_REDIS_PORT", 6379),
				Password: getEnv("MEMORY_OS_REDIS_PASSWORD", ""),
				DB:       getEnvInt("MEMORY_OS_REDIS_DB", 0),
			},
			MongoDB: MongoDBConfig{
				URI:      getEnv("MEMORY_OS_MONGODB_URI", "mongodb://localhost:27017/memory_os?retryWrites=true&authSource=memory_os"),
				Database: getEnv("MEMORY_OS_MONGODB_DATABASE", "memory_os"),
				Username: getEnv("MEMORY_OS_MONGODB_USERNAME", ""),
				Password: getEnv("MEMORY_OS_MONGODB_PASSWORD", ""),
			},
			Milvus: MilvusConfig{
				Host: getEnv("MEMORY_OS_MILVUS_HOST", "localhost"),
				Port: getEnvInt("MEMORY_OS_MILVUS_PORT", 19530),
			},
		},
	}

	apiKeys, err := parseAPIKeys(os.Getenv("MEMORY_OS_API_KEYS"), os.Getenv("MEMORY_OS_API_KEY"))
	if err != nil {
		return nil, err
	}
	config.Auth.APIKeys = apiKeys

	return config, nil
}

// Helper functions for environment variables
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Server.GRPCPort <= 0 || c.Server.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.Server.GRPCPort)
	}

	if c.Auth.RequireJWT && c.Auth.JWTSecret == "" {
		return fmt.Errorf("MEMORY_OS_JWT_SECRET is required when JWT authentication is enabled")
	}
	if c.Auth.RequireJWT && c.Auth.JWTSecret == placeholderJWTSecret {
		return fmt.Errorf("MEMORY_OS_JWT_SECRET is the published placeholder %q; set a secret of your own", placeholderJWTSecret)
	}

	if c.Auth.RequireAPIKey && len(c.Auth.APIKeys) == 0 {
		return fmt.Errorf("MEMORY_OS_API_KEYS (or MEMORY_OS_API_KEY) is required when API key authentication is enabled")
	}

	if !c.Auth.RequireAPIKey && !c.Auth.RequireJWT && !c.Auth.AllowUnauthenticated {
		return fmt.Errorf("authentication is disabled: set MEMORY_OS_REQUIRE_API_KEY and/or MEMORY_OS_REQUIRE_JWT, " +
			"or MEMORY_OS_ALLOW_UNAUTHENTICATED=true to run without it (any caller can then read every tenant's memory)")
	}

	if c.Database.MongoDB.URI == "" {
		return fmt.Errorf("MongoDB URI is required")
	}

	return nil
}
