package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type HistoryPoint struct {
	FetchedAt time.Time
	Balance   float64
}

func OpenStore(path string) (*Store, error) {
	// _busy_timeout helps if multiple processes ever share this DB file.
	// _journal_mode=WAL keeps writers from blocking the hourly reader.
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS balance_history (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			account_no   TEXT    NOT NULL,
			meter_no     TEXT,
			balance      REAL    NOT NULL,
			min_recharge REAL,
			fetched_at   TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_balance_history_account_time
			ON balance_history(account_no, fetched_at);

		CREATE TABLE IF NOT EXISTS recharges (
			order_id      TEXT PRIMARY KEY,
			account_no    TEXT NOT NULL,
			token         TEXT,
			energy_amount REAL NOT NULL,
			paid_amount   REAL,
			purchased_at  TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_recharges_account_time
			ON recharges(account_no, purchased_at);
	`)
	return err
}

// Insert persists a snapshot. fetched_at is stored as UTC.
func (s *Store) Insert(ctx context.Context, snap *BalanceSnapshot) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO balance_history (account_no, meter_no, balance, min_recharge, fetched_at)
		VALUES (?, ?, ?, ?, ?)`,
		snap.AccountNo, snap.MeterNo, snap.Balance, snap.MinRecharge, snap.FetchedAt.UTC(),
	)
	return err
}

// History returns points for an account within the lookback window, oldest first.
func (s *Store) History(ctx context.Context, accountNo string, lookback time.Duration) ([]HistoryPoint, error) {
	since := time.Now().UTC().Add(-lookback)
	rows, err := s.db.QueryContext(ctx, `
		SELECT fetched_at, balance
		FROM balance_history
		WHERE account_no = ? AND fetched_at >= ?
		ORDER BY fetched_at ASC`,
		accountNo, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		var t time.Time
		if err := rows.Scan(&t, &p.Balance); err != nil {
			return nil, err
		}
		p.FetchedAt = t.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertRecharges inserts new recharges, ignoring ones already stored
// (dedup on order_id). purchased_at is stored UTC.
func (s *Store) UpsertRecharges(ctx context.Context, accountNo string, rs []Recharge) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO recharges (order_id, account_no, token, energy_amount, paid_amount, purchased_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(order_id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rs {
		if _, err := stmt.ExecContext(ctx, r.OrderID, accountNo, r.Token, r.EnergyAmount, r.PaidAmount, r.PurchasedAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RechargesSince returns recharges for an account within the lookback window,
// oldest first.
func (s *Store) RechargesSince(ctx context.Context, accountNo string, lookback time.Duration) ([]Recharge, error) {
	since := time.Now().UTC().Add(-lookback)
	rows, err := s.db.QueryContext(ctx, `
		SELECT order_id, token, energy_amount, paid_amount, purchased_at
		FROM recharges
		WHERE account_no = ? AND purchased_at >= ?
		ORDER BY purchased_at ASC`, accountNo, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recharge
	for rows.Next() {
		var r Recharge
		var t time.Time
		if err := rows.Scan(&r.OrderID, &r.Token, &r.EnergyAmount, &r.PaidAmount, &t); err != nil {
			return nil, err
		}
		r.PurchasedAt = t.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// PreviousReading returns the most recent reading (balance + time) strictly
// before `at`. Returns ok=false if none exists.
func (s *Store) PreviousReading(ctx context.Context, accountNo string, at time.Time) (float64, time.Time, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT balance, fetched_at FROM balance_history
		WHERE account_no = ? AND fetched_at < ?
		ORDER BY fetched_at DESC LIMIT 1`, accountNo, at.UTC())
	var bal float64
	var t time.Time
	switch err := row.Scan(&bal, &t); err {
	case nil:
		return bal, t.UTC(), true, nil
	case sql.ErrNoRows:
		return 0, time.Time{}, false, nil
	default:
		return 0, time.Time{}, false, err
	}
}
