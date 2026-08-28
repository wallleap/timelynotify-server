package database

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/lithammer/shortuuid/v3"
	"github.com/mritd/logger"
)

type MySQL struct {
}

var mysqlDB *sql.DB

const (
	dbSchema = "" +
		"CREATE TABLE IF NOT EXISTS `devices` (" +
		"    `id` INT UNSIGNED NOT NULL AUTO_INCREMENT," +
		"    `key` VARCHAR(255) NOT NULL," +
		"    `token` VARCHAR(255) NOT NULL," +
		"    `platform` VARCHAR(32) NOT NULL DEFAULT 'ios'," +
		"    PRIMARY KEY (`id`)," +
		"    UNIQUE KEY `key_platform` (`key`, `platform`)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
)

// migrations upgrade pre-multi-platform tables to the new schema. Errors are
// logged at warn level and ignored — each statement is idempotent enough that
// a duplicate-column or duplicate-key error just means the migration already
// ran. Statements use MySQL 8 / MariaDB syntax; on MySQL 5.7 the IF EXISTS /
// IF NOT EXISTS clauses are unsupported and the statements error out, which
// is fine because the corresponding ALTER has already been applied.
var migrations = []string{
	"ALTER TABLE `devices` ADD COLUMN IF NOT EXISTS `platform` VARCHAR(32) NOT NULL DEFAULT 'ios'",
	"ALTER TABLE `devices` DROP INDEX IF EXISTS `key`",
	"ALTER TABLE `devices` ADD UNIQUE KEY IF NOT EXISTS `key_platform` (`key`, `platform`)",
}

func NewMySQL(dsn string) Database {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		logger.Fatalf("failed to open database connection (%s): %v", dsn, err)
	}

	_, err = db.Exec(dbSchema)
	if err != nil {
		logger.Fatalf("failed to init database schema(%s): %v", dbSchema, err)
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			logger.Warnf("mysql migration skipped (%s): %v", m, err)
		}
	}

	mysqlDB = db
	return &MySQL{}
}

func NewMySQLWithTLS(dsn, tlsName, caPath, certPath, keyPath string, isSkipVerify bool) Database {
	// 1. Load and register TLS configuration
	logger.Infof("MySQL TLS CA: %v", caPath)
	logger.Infof("MySQL TLS client cert: %v", certPath)
	logger.Infof("MySQL TLS client key: %v", keyPath)
	logger.Infof("Server certificate verification skipped: %v", isSkipVerify)
	rootCertPool := x509.NewCertPool()
	pem, err := os.ReadFile(caPath)
	if err != nil {
		logger.Fatalf("failed to read CA cert: %v", err)
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		logger.Fatalf("failed to append CA cert")
	}

	var certs []tls.Certificate
	if certPath != "" && keyPath != "" {
		clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			logger.Fatalf("failed to load client cert and key: %v", err)
		}
		certs = []tls.Certificate{clientCert}
	}

	tlsConfig := &tls.Config{
		RootCAs:            rootCertPool,
		Certificates:       certs,
		InsecureSkipVerify: isSkipVerify,
	}

	if err := mysql.RegisterTLSConfig(tlsName, tlsConfig); err != nil {
		logger.Fatalf("failed to register TLS config: %v", err)
	}

	// 2. Append TLS parameter to DSN if missing
	if !strings.Contains(dsn, "tls=") {
		if strings.Contains(dsn, "?") {
			dsn = dsn + "&tls=" + tlsName
		} else {
			dsn = dsn + "?tls=" + tlsName
		}
	}

	// 3. Create and return the Database instance
	return NewMySQL(dsn)
}

func (d *MySQL) CountAll() (int, error) {
	var count int
	err := mysqlDB.QueryRow("SELECT COUNT(1) FROM `devices`").Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// DeviceTokenByKey returns any non-empty token for the key, preferring ios.
func (d *MySQL) DeviceTokenByKey(key string) (string, error) {
	var token string
	err := mysqlDB.QueryRow(
		"SELECT `token` FROM `devices` WHERE `key`=? ORDER BY (`platform`='ios') DESC LIMIT 1", key,
	).Scan(&token)
	if err != nil {
		return "", err
	}
	// An empty token means the key is known but the device is no longer valid
	// (e.g. wiped by BadDeviceToken cleanup). Report it as invalid like bbolt
	// does, instead of returning "" with nil so the caller keeps pushing on
	// a dead token.
	if len(token) == 0 {
		return "", fmt.Errorf("device token invalid")
	}
	return token, nil
}

// SaveDeviceTokenByKey is the legacy upsert (defaults to platform "ios").
func (d *MySQL) SaveDeviceTokenByKey(key, token string) (string, error) {
	return d.SaveDeviceInfo(&DeviceInfo{
		Key:      key,
		Token:    token,
		Platform: "ios",
	})
}

// DevicesByKey returns every (key, platform) record for the key. iOS is
// ordered first so callers that only consume the head see a stable record.
func (d *MySQL) DevicesByKey(key string) ([]*DeviceInfo, error) {
	rows, err := mysqlDB.Query(
		"SELECT `token`, `platform` FROM `devices` WHERE `key`=? ORDER BY (`platform`='ios') DESC", key,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var infos []*DeviceInfo
	for rows.Next() {
		var info DeviceInfo
		if err := rows.Scan(&info.Token, &info.Platform); err != nil {
			return nil, err
		}
		info.Key = key
		infos = append(infos, &info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("failed to get [%s] device info from database", key)
	}
	return infos, nil
}

// DeviceInfoByKey returns the first record for the key, preferring "ios".
// Deprecated: use DevicesByKey for multi-platform fan-out.
func (d *MySQL) DeviceInfoByKey(key string) (*DeviceInfo, error) {
	var info DeviceInfo
	err := mysqlDB.QueryRow(
		"SELECT `token`, `platform` FROM `devices` WHERE `key`=? ORDER BY (`platform`='ios') DESC LIMIT 1", key,
	).Scan(&info.Token, &info.Platform)
	if err != nil {
		return nil, err
	}
	if len(info.Token) == 0 {
		return nil, fmt.Errorf("device token invalid")
	}
	info.Key = key
	return &info, nil
}

// SaveDeviceInfo upserts by (key, platform). Re-registering the same
// (key, platform) updates the token; a different platform adds a new row
// without touching the others.
func (d *MySQL) SaveDeviceInfo(info *DeviceInfo) (string, error) {
	if info.Platform == "" {
		info.Platform = "ios"
	}
	if info.Key == "" {
		info.Key = shortuuid.New()
	}
	_, err := mysqlDB.Exec(
		"INSERT INTO `devices` (`key`,`token`,`platform`) VALUES (?,?,?) "+
			"ON DUPLICATE KEY UPDATE `token`=VALUES(`token`)",
		info.Key, info.Token, info.Platform,
	)
	if err != nil {
		return "", err
	}
	return info.Key, nil
}

// ClearDeviceTokenByKeyAndPlatform empties the token of the given (key,
// platform) pair. The row itself is kept so the key remains known and other
// platforms are untouched.
func (d *MySQL) ClearDeviceTokenByKeyAndPlatform(key, platform string) error {
	if platform == "" {
		platform = "ios"
	}
	_, err := mysqlDB.Exec(
		"UPDATE `devices` SET `token`='' WHERE `key`=? AND `platform`=?",
		key, platform,
	)
	return err
}

func (d *MySQL) DeleteDeviceByKey(key string) error {
	_, err := mysqlDB.Exec("DELETE FROM `devices` WHERE `key`=?", key)
	return err
}

func (d *MySQL) Close() error {
	return mysqlDB.Close()
}
