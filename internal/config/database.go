package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

// InitDatabase initializes the GORM database connection
// Reads DATABASE_URL from environment and connects to MySQL
func InitDatabase() error {
	log := GetLogger()

	// Get DATABASE_URL from environment (required)
	dbURL, err := GetEnvRequired("DATABASE_URL")
	if err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Parse MySQL DSN from DATABASE_URL format: mysql://user:password@tcp(host:port)/database?params
	dsn, err := parseMySQLDSN(dbURL)
	if err != nil {
		return fmt.Errorf("failed to parse DATABASE_URL: %w", err)
	}

	// Configure GORM logger based on environment
	var gormLogger logger.Interface
	env := GetEnv("ENV", "development")
	if env == "production" {
		// Production: Only log errors
		gormLogger = logger.Default.LogMode(logger.Error)
	} else {
		// Development: Log all queries (with warnings for slow queries)
		gormLogger = logger.Default.LogMode(logger.Info)
	}

	// Open database connection
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: gormLogger,
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get underlying sql.DB for connection pool configuration
	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(25)                 // Maximum number of open connections
	sqlDB.SetMaxIdleConns(10)                 // Maximum number of idle connections
	sqlDB.SetConnMaxLifetime(5 * time.Minute) // Maximum connection lifetime
	sqlDB.SetConnMaxIdleTime(10 * time.Minute) // Maximum idle time

	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	db = database
	log.Info("Database connection established successfully")
	return nil
}

// GetDB returns the singleton database instance
// Must call InitDatabase() first
func GetDB() *gorm.DB {
	if db == nil {
		// Return a nil-safe instance - will panic on use if not initialized
		// This is intentional to catch initialization errors early
		log := GetLogger()
		log.Error("Database not initialized. Call InitDatabase() first.")
	}
	return db
}

// CloseDatabase closes the database connection gracefully
func CloseDatabase() error {
	if db == nil {
		return nil
	}

	log := GetLogger()
	sqlDB, err := db.DB()
	if err != nil {
		log.Error("Failed to get underlying sql.DB for closing", zap.Error(err))
		return err
	}

	if err := sqlDB.Close(); err != nil {
		log.Error("Failed to close database connection", zap.Error(err))
		return err
	}

	log.Info("Database connection closed")
	return nil
}

// parseMySQLDSN converts mysql:// URL format to GORM MySQL DSN format
// Input: mysql://user:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
// Output: user:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
func parseMySQLDSN(dbURL string) (string, error) {
	// Handle both mysql:// and direct DSN formats
	if !strings.HasPrefix(dbURL, "mysql://") {
		// Already in DSN format
		return dbURL, nil
	}

	// Remove mysql:// prefix
	dsn := strings.TrimPrefix(dbURL, "mysql://")

	// Parse URL to extract components
	parsedURL, err := url.Parse("mysql://" + dsn)
	if err != nil {
		// If parsing fails, try direct regex-based parsing for tcp() format
		return parseMySQLDSNRegex(dbURL)
	}

	// Reconstruct DSN from parsed URL
	user := parsedURL.User.Username()
	password, hasPassword := parsedURL.User.Password()
	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "3306" // Default MySQL port
	}
	database := strings.TrimPrefix(parsedURL.Path, "/")

	// Build DSN
	var dsnBuilder strings.Builder
	if hasPassword {
		dsnBuilder.WriteString(fmt.Sprintf("%s:%s@", user, password))
	} else {
		dsnBuilder.WriteString(fmt.Sprintf("%s@", user))
	}
	dsnBuilder.WriteString(fmt.Sprintf("tcp(%s:%s)", host, port))
	dsnBuilder.WriteString(fmt.Sprintf("/%s", database))

	// Add query parameters (charset, parseTime, etc.)
	if parsedURL.RawQuery != "" {
		dsnBuilder.WriteString("?")
		dsnBuilder.WriteString(parsedURL.RawQuery)
	}

	return dsnBuilder.String(), nil
}

// parseMySQLDSNRegex handles mysql:// URLs with tcp() format directly in the host
// Format: mysql://user:password@tcp(host:port)/database?params
func parseMySQLDSNRegex(dbURL string) (string, error) {
	// Regex to match: mysql://user:password@tcp(host:port)/database?params
	re := regexp.MustCompile(`^mysql://([^:]+)(?::([^@]+))?@tcp\(([^)]+)\)/([^?]+)(?:\?(.*))?$`)
	matches := re.FindStringSubmatch(dbURL)

	if len(matches) < 5 {
		return "", fmt.Errorf("invalid DATABASE_URL format: %s. Expected: mysql://user:password@tcp(host:port)/database?params", dbURL)
	}

	user := matches[1]
	password := matches[2]
	hostPort := matches[3]
	database := matches[4]
	params := matches[5]

	// Build DSN
	var dsnBuilder strings.Builder
	if password != "" {
		dsnBuilder.WriteString(fmt.Sprintf("%s:%s@", user, password))
	} else {
		dsnBuilder.WriteString(fmt.Sprintf("%s@", user))
	}
	dsnBuilder.WriteString(fmt.Sprintf("tcp(%s)", hostPort))
	dsnBuilder.WriteString(fmt.Sprintf("/%s", database))

	if params != "" {
		dsnBuilder.WriteString("?")
		dsnBuilder.WriteString(params)
	}

	return dsnBuilder.String(), nil
}

