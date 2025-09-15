package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Site     SiteConfig
	Database DatabaseConfig
	JWT      JWTConfig
}

var cfg *Config

func GetConfig() *Config {
	return cfg
}

type ServerConfig struct {
	Port string
	Mode string // debug or release
}

type SiteConfig struct {
	SiteUrl           string
	SiteName          string
	VERSION           string
	TrackerScriptName string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
	TimeZone string
}

type JWTConfig struct {
	SecretKey     string
	ExpireHours   int
	RefreshExpire int
}

func Load() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Warning: Could not load .env file. Proceeding with environment variables.")
	}
	cfg = &Config{
		Server: ServerConfig{
			Port: getEnv("SERVER_PORT", "8080"),
			Mode: getEnv("GIN_MODE", "debug"),
		},
		Site: SiteConfig{
			SiteUrl:           getEnv("SITE_DOMAIN", "http://localhost:5004"),
			SiteName:          getEnv("SITE_NAME", "Pingoo"),
			VERSION:           getEnv("VERSION", "1.0.0"),
			TrackerScriptName: getEnv("TRACKER_SCRIPT_NAME", "pingoo.js"),
		},
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "pingoo"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
			TimeZone: getEnv("TIME_ZONE", "Asia/Shanghai"),
		},
		JWT: JWTConfig{
			SecretKey:     getEnv("JWT_SECRET_KEY", "your-secret-key-change-this-in-production"),
			ExpireHours:   getEnvAsInt("JWT_EXPIRE_HOURS", 24),
			RefreshExpire: getEnvAsInt("JWT_REFRESH_EXPIRE", 168),
		},
	}
	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
