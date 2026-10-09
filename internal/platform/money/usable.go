package money

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CurrencyInfo describes a usable currency (minor units drive client-side amount parsing).
type CurrencyInfo struct {
	Code          string `json:"code"`
	MinorUnits    int    `json:"minor_units"`
	DisplaySymbol string `json:"display_symbol"`
}

// UsableCurrencyInfo is UsableCurrencies with minor units and symbols.
func UsableCurrencyInfo(ctx context.Context, q Querier) ([]CurrencyInfo, error) {
	rows, err := q.Query(ctx, `SELECT code, minor_units, display_symbol FROM app.currencies WHERE enabled AND minor_units_verified ORDER BY code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CurrencyInfo, error) {
		var c CurrencyInfo
		var mu int16
		err := r.Scan(&c.Code, &mu, &c.DisplaySymbol)
		c.MinorUnits = int(mu)
		return c, err
	})
}

// UsableCurrencies lists the currencies that may carry amounts now: enabled AND with verified minor units
// (MONEY.md §2). ZWG stays unusable until its minor units are verified (LR-043).
func UsableCurrencies(ctx context.Context, q Querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT code FROM app.currencies WHERE enabled AND minor_units_verified ORDER BY code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
