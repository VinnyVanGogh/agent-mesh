package telemetry

import (
	"database/sql"
	"log/slog"
	"math"
)

// BackfillCosts updates cost_usd and cost_usd_micros for every request row that
// has token counts but a NULL stored cost. It processes up to batchSize rows per
// call so it can be invoked periodically without holding a long write lock.
// Returns the number of rows updated.
func BackfillCosts(db *sql.DB) (int64, error) {
	return backfillCostsBatch(db, 2000)
}

func backfillCostsBatch(db *sql.DB, batchSize int) (int64, error) {
	rows, err := db.Query(`
		SELECT rowid, model,
		       COALESCE(input_tokens, 0),
		       COALESCE(output_tokens, 0),
		       COALESCE(cache_read_tokens, 0),
		       COALESCE(cache_creation_tokens, 0)
		FROM requests
		WHERE cost_usd IS NULL
		  AND (input_tokens > 0 OR output_tokens > 0)
		LIMIT ?
	`, batchSize)
	if err != nil {
		return 0, err
	}

	type rec struct {
		rowid       int64
		model       string
		input       int64
		output      int64
		cacheRead   int64
		cacheCreate int64
	}
	var pending []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.rowid, &r.model, &r.input, &r.output, &r.cacheRead, &r.cacheCreate); err == nil {
			pending = append(pending, r)
		}
	}
	rows.Close()

	if len(pending) == 0 {
		return 0, nil
	}

	stmt, err := db.Prepare(`
		UPDATE requests SET cost_usd = ?, cost_usd_micros = ?
		WHERE rowid = ? AND cost_usd IS NULL
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var updated int64
	for _, r := range pending {
		micros, priced := ComputeCostMicros(r.model, Usage{
			Input:           r.input,
			Output:          r.output,
			CacheRead:       r.cacheRead,
			CacheCreation5m: r.cacheCreate,
		})
		var costUSD float64
		if priced {
			costUSD = float64(micros) / 1_000_000.0
		} else {
			costUSD = EstimateModelCost(r.model, r.input, r.output, r.cacheRead, r.cacheCreate)
			micros = int64(math.Round(costUSD * 1_000_000.0))
		}
		if costUSD <= 0 {
			continue
		}
		res, execErr := stmt.Exec(costUSD, micros, r.rowid)
		if execErr != nil {
			slog.Debug("backfill cost update failed",
				slog.Int64("rowid", r.rowid),
				slog.String("model", r.model),
				slog.Any("error", execErr))
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	return updated, nil
}
