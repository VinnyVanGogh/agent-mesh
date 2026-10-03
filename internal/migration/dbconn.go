// Package migration — dbconn.go
// RunChecks executes a set of verification checks inside a READ ONLY Postgres
// transaction. The connection string is caller-supplied; the caller reads it from
// the macOS Keychain (keychain_darwin.go) or another source.
//
// Transaction envelope:
//   BEGIN READ ONLY;
//   SET LOCAL statement_timeout = '5s';
//   … SELECT … AS ok;
//   ROLLBACK;
//
// A write attempt inside the transaction returns an error, proving read-only.
package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq" // postgres driver
)

// RunVerifyTimeout is the statement-level timeout applied inside the read-only tx.
const RunVerifyTimeout = 5 * time.Second

// RunChecks opens a connection to dsn, runs each check inside a read-only
// transaction, and returns the results. It never writes to the database.
// The connection is closed before returning.
func RunChecks(ctx context.Context, dsn string, checks []Check) ([]CheckResult, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	// Confirm connection works with a short dial timeout.
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read only: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = '%dms'", RunVerifyTimeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("set timeout: %w", err)
	}

	results := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		r := CheckResult{Check: c}

		if c.Kind == KindUnchecked {
			r.Passed = false
			r.Error = "not checked"
			results = append(results, r)
			continue
		}

		var ok sql.NullBool
		row := tx.QueryRowContext(ctx, c.SQL)
		if err := row.Scan(&ok); err != nil {
			r.Passed = false
			r.Error = err.Error()
		} else {
			r.Passed = ok.Valid && ok.Bool
		}
		results = append(results, r)
	}

	return results, nil
}

// AllPassed returns true if every result passed (no failures or unchecked items
// that should block).
func AllPassed(results []CheckResult) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// FailedDescriptions returns descriptions of all non-passing checks.
func FailedDescriptions(results []CheckResult) []string {
	var out []string
	for _, r := range results {
		if !r.Passed {
			out = append(out, r.Description)
		}
	}
	return out
}
