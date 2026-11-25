//go:build !noduckdb && cgo

package container

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/Tencent/WeKnora/internal/logger"
)

func NewDuckDB() (*sql.DB, error) {
	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open duckdb: %w", err)
	}

	// Try to install and load required extensions unless explicitly disabled.
	//   - spatial: used for st_read_meta() to enumerate layer (sheet) names from .xlsx/.xls
	//   - excel:   used for read_xlsx() which gives proper type inference per sheet
	//
	// INSTALL hits extensions.duckdb.org (public internet). In locked-down
	// runtimes with no egress, set DUCKDB_SKIP_EXTENSION_LOAD=1 to avoid a
	// startup hang; xlsx/xls ingest may fail later without these extensions.
	if strings.EqualFold(os.Getenv("DUCKDB_SKIP_EXTENSION_LOAD"), "true") ||
		os.Getenv("DUCKDB_SKIP_EXTENSION_LOAD") == "1" {
		logger.Infof(context.Background(),
			"[DuckDB] Skipping spatial/excel extension install/load "+
				"(DUCKDB_SKIP_EXTENSION_LOAD is set; xlsx ingest may fail without them)")
	} else {
		bgCtx := context.Background()
		for _, ext := range []string{"spatial", "excel"} {
			if _, err := sqlDB.ExecContext(bgCtx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
				logger.Warnf(bgCtx, "[DuckDB] Failed to install %s extension: %v", ext, err)
			}
			if _, err := sqlDB.ExecContext(bgCtx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
				logger.Warnf(bgCtx, "[DuckDB] Failed to load %s extension: %v", ext, err)
			}
		}
	}

	return sqlDB, nil
}
