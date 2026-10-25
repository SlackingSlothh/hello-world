package manageddb

import (
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// normalizeDriver accepts common aliases while retaining a canonical public name.
func normalizeDriver(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgres", "postgresql", "pgx":
		return "postgres"
	case "mysql":
		return "mysql"
	case "mariadb", "maria":
		return "mariadb"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// sqlDriverName maps public connection types to explicitly registered SQL
// drivers. MariaDB speaks the MySQL protocol and uses the MySQL driver.
func sqlDriverName(name string) (string, bool) {
	switch normalizeDriver(name) {
	case "postgres":
		return "pgx", true
	case "mysql", "mariadb":
		return "mysql", true
	default:
		return "", false
	}
}
