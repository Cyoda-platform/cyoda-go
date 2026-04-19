package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

// Compile-time check that entityStore implements spi.Searcher.
var _ spi.Searcher = (*entityStore)(nil)

// ErrScanBudgetExhausted is returned when a search with a residual filter
// examines more rows than the configured SearchScanLimit without filling
// the requested result page. Callers should tighten their filter.
var ErrScanBudgetExhausted = errors.New("scan budget exhausted")

// Search implements spi.Searcher for the SQLite entity store.
// It uses the query planner to push down pushable predicates to SQL and
// post-filters the residual in Go. Pagination is applied in SQL when no
// residual exists, or in Go after post-filtering.
func (s *entityStore) Search(ctx context.Context, filter spi.Filter, opts spi.SearchOptions) ([]*spi.Entity, error) {
	if err := validateFilterPaths(filter); err != nil {
		return nil, err
	}
	if err := validateOrderSpecs(opts.OrderBy); err != nil {
		return nil, err
	}
	plan := planQuery(filter)

	var baseQuery string
	var baseArgs []any

	if opts.PointInTime != nil {
		baseQuery, baseArgs = s.searchPointInTimeBase(opts)
	} else {
		baseQuery, baseArgs = s.searchCurrentStateBase(opts)
	}

	if plan.where != "" {
		baseQuery += " AND (" + plan.where + ")"
		baseArgs = append(baseArgs, plan.args...)
	}

	if opts.PointInTime != nil {
		baseQuery += orderByClause(opts, "ev")
	} else {
		baseQuery += orderByClause(opts, "")
	}

	// When there is no residual, apply LIMIT/OFFSET in SQL.
	if plan.postFilter == nil {
		if opts.Limit > 0 {
			baseQuery += " LIMIT ?"
			baseArgs = append(baseArgs, opts.Limit)
		}
		if opts.Offset > 0 {
			baseQuery += " OFFSET ?"
			baseArgs = append(baseArgs, opts.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, baseQuery, baseArgs...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []*spi.Entity
	scanned := 0

	for rows.Next() {
		if plan.postFilter != nil && scanned >= s.cfg.SearchScanLimit {
			return nil, fmt.Errorf("%w: examined %d rows", ErrScanBudgetExhausted, s.cfg.SearchScanLimit)
		}
		scanned++

		var e *spi.Entity
		var scanErr error
		if opts.PointInTime != nil {
			e, scanErr = scanVersionEntity(rows)
		} else {
			e, scanErr = scanEntityFromRow(rows)
		}
		if scanErr != nil {
			return nil, scanErr
		}

		if plan.postFilter != nil {
			matches, evalErr := evaluateFilter(*plan.postFilter, e)
			if evalErr != nil {
				return nil, fmt.Errorf("post-filter evaluation: %w", evalErr)
			}
			if !matches {
				continue
			}
		}

		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration: %w", err)
	}

	// Apply offset and limit in Go when post-filtering was active.
	if plan.postFilter != nil {
		if opts.Offset > 0 {
			if opts.Offset >= len(results) {
				return nil, nil
			}
			results = results[opts.Offset:]
		}
		if opts.Limit > 0 && len(results) > opts.Limit {
			results = results[:opts.Limit]
		}
	}

	return results, nil
}

// searchCurrentStateBase returns the base SQL for current-state search.
func (s *entityStore) searchCurrentStateBase(opts spi.SearchOptions) (string, []any) {
	query := `SELECT entity_id, model_name, model_version, version,
	                 json(data), json(meta), created_at, updated_at
	          FROM entities
	          WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND NOT deleted`
	args := []any{string(s.tenantID), opts.ModelName, opts.ModelVersion}
	return query, args
}

// searchPointInTimeBase returns the base SQL for point-in-time search.
// Uses submit_time <= ? (non-strict) matching the memory plugin's convention
// (!v.submitTime.After(snapshotTime)) and all other snapshot queries in this
// package (getSnapshot, getAllTx, DeleteAll tx).
func (s *entityStore) searchPointInTimeBase(opts spi.SearchOptions) (string, []any) {
	pit := timeToMicro(*opts.PointInTime)
	query := `SELECT ev.entity_id, ev.model_name, ev.model_version, ev.version,
	                 json(ev.data), json(ev.meta), ev.submit_time
	          FROM entity_versions ev
	          INNER JOIN (
	              SELECT entity_id, MAX(version) AS max_ver
	              FROM entity_versions
	              WHERE tenant_id = ? AND model_name = ? AND model_version = ? AND submit_time <= ?
	              GROUP BY entity_id
	          ) latest ON ev.entity_id = latest.entity_id AND ev.version = latest.max_ver
	          WHERE ev.tenant_id = ? AND ev.change_type != 'DELETED'`
	args := []any{string(s.tenantID), opts.ModelName, opts.ModelVersion, pit, string(s.tenantID)}
	return query, args
}

// orderByClause builds a SQL ORDER BY clause from opts.OrderBy.
// When OrderBy is empty, defaults to "ORDER BY entity_id".
// tablePrefix is prepended to direct column names (e.g., "ev" for point-in-time
// queries where the entity_versions table is aliased as "ev").
// For SourceMeta paths that are direct columns, uses the column name.
// For SourceMeta paths in the meta BLOB, uses json_extract(json(meta), '$.path').
// For SourceData paths, uses json_extract(data, '$.path').
func orderByClause(opts spi.SearchOptions, tablePrefix string) string {
	if len(opts.OrderBy) == 0 {
		col := "entity_id"
		if tablePrefix != "" {
			col = tablePrefix + "." + col
		}
		return " ORDER BY " + col
	}
	clauses := make([]string, 0, len(opts.OrderBy))
	for _, spec := range opts.OrderBy {
		expr := orderByFieldExpr(spec, tablePrefix)
		if spec.Desc {
			expr += " DESC"
		}
		clauses = append(clauses, expr)
	}
	return " ORDER BY " + strings.Join(clauses, ", ")
}

// orderByFieldExpr returns the SQL expression for an OrderSpec field.
//
// Safety invariant: spec.Path is interpolated into a JSON-path literal
// and therefore MUST have been validated by validateOrderSpecs at the
// Search() boundary (see path_validation.go). Adding a new caller that
// bypasses Search() re-introduces SQL injection.
func orderByFieldExpr(spec spi.OrderSpec, tablePrefix string) string {
	qualify := func(col string) string {
		if tablePrefix != "" {
			return tablePrefix + "." + col
		}
		return col
	}
	if spec.Source == spi.SourceMeta {
		if directMetaColumns[spec.Path] {
			return qualify(spec.Path)
		}
		return fmt.Sprintf("json_extract(json(%s), '$.%s')", qualify("meta"), spec.Path)
	}
	return fmt.Sprintf("json_extract(%s, '$.%s')", qualify("data"), spec.Path)
}
