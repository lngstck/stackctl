// Package postgres handles creating per-app databases and users in the
// shared ls-postgres container. Apps that list "postgres" in their
// depends_on get their own database (app_id), user (app_id), and a
// generated password stored in .env under {APP_ID_UPPER}_DB_PASSWORD.
//
// All interaction is via docker exec ls-postgres psql — no PG driver needed
// in stackctl itself, keeping the dependency count at zero for this package.
package postgres

import (
	"fmt"
	"strings"

	"github.com/lngstck/stackctl/internal/docker"
)

// ContainerName is the Docker name of the shared Postgres container.
const ContainerName = "ls-postgres"

// SetupAppDB creates a Postgres user and database for appID. The password
// should be pre-generated and stored in .env before calling this.
//
// The function is idempotent: running it twice with the same arguments
// does not error (CREATE ... IF NOT EXISTS, ALTER for the password).
//
// Returns an error if the Postgres container is not reachable.
func SetupAppDB(appID, password string) error {
	user := sanitizeIdent(appID)
	db := sanitizeIdent(appID)

	// Create user (or update password if user already exists).
	createUser := fmt.Sprintf(
		"DO $$ BEGIN "+
			"IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN "+
			"CREATE ROLE \"%s\" LOGIN PASSWORD '%s'; "+
			"ELSE ALTER ROLE \"%s\" PASSWORD '%s'; "+
			"END IF; END $$;",
		user, user, escapeSingleQuote(password),
		user, escapeSingleQuote(password),
	)
	if err := psql(createUser); err != nil {
		return fmt.Errorf("create user %s: %w", user, err)
	}

	// Create database owned by the user. CREATE DATABASE cannot run inside a
	// transaction and has no IF NOT EXISTS, so we check existence first via a
	// separate query. (psql's \gexec meta-command does not work reliably with
	// `psql -c`, which is why we split it into two calls.)
	exists, err := databaseExists(db)
	if err != nil {
		return fmt.Errorf("check database %s: %w", db, err)
	}
	if !exists {
		createDB := fmt.Sprintf(`CREATE DATABASE "%s" OWNER "%s"`, db, user)
		if err := psql(createDB); err != nil {
			return fmt.Errorf("create database %s: %w", db, err)
		}
	}

	// Grant all privileges on the database to the user.
	grant := fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE "%s" TO "%s"`, db, user)
	if err := psql(grant); err != nil {
		return fmt.Errorf("grant on %s: %w", db, err)
	}

	return nil
}

// DropAppDB removes the database and user for appID. Idempotent — no error
// if they don't exist. Used during app removal.
func DropAppDB(appID string) error {
	user := sanitizeIdent(appID)
	db := sanitizeIdent(appID)

	dropDB := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, db)
	if err := psql(dropDB); err != nil {
		return fmt.Errorf("drop database %s: %w", db, err)
	}

	dropUser := fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, user)
	if err := psql(dropUser); err != nil {
		return fmt.Errorf("drop role %s: %w", user, err)
	}
	return nil
}

// IsReachable checks if the Postgres container is running and accepting
// connections.
func IsReachable() bool {
	code, _, _ := docker.Exec(ContainerName, []string{
		"pg_isready", "-U", "postgres",
	})
	return code == 0
}

// DBPasswordEnvKey returns the conventional .env key name for an app's
// database password: "{APP_ID_UPPER}_DB_PASSWORD".
func DBPasswordEnvKey(appID string) string {
	return strings.ToUpper(strings.ReplaceAll(appID, "-", "_")) + "_DB_PASSWORD"
}

// -- internals --------------------------------------------------------------

// psql runs a SQL statement inside the Postgres container via docker exec.
func psql(sql string) error {
	code, output, err := docker.Exec(ContainerName, []string{
		"psql", "-U", "postgres", "-c", sql,
	})
	if err != nil {
		return fmt.Errorf("docker exec: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("psql exit %d: %s", code, output)
	}
	return nil
}

// databaseExists returns true if a database with the given name exists.
func databaseExists(db string) (bool, error) {
	// -tAc: tuples-only, unaligned, run single command. Output is "1" or "".
	code, output, err := docker.Exec(ContainerName, []string{
		"psql", "-U", "postgres", "-tAc",
		fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", escapeSingleQuote(db)),
	})
	if err != nil {
		return false, fmt.Errorf("docker exec: %w", err)
	}
	if code != 0 {
		return false, fmt.Errorf("psql exit %d: %s", code, output)
	}
	return strings.TrimSpace(output) == "1", nil
}

// sanitizeIdent ensures the identifier is safe for PG. App IDs are already
// validated as [a-z0-9-] by the catalog, so this is a defense-in-depth
// measure: strip anything that isn't alphanumeric or underscore.
func sanitizeIdent(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "-", "_"))
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	if result == "" {
		return "unknown"
	}
	return result
}

// escapeSingleQuote doubles single quotes for use inside SQL string
// literals (standard SQL escaping).
func escapeSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
