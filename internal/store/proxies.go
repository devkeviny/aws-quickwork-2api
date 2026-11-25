package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"quickwork-openai/internal/outbound"
)

var ErrProxyInUse = errors.New("proxy is bound to an account")

func (s *Store) ImportProxies(ctx context.Context, text string) (int64, error) {
	proxies, err := outbound.ParseList(text)
	if err != nil {
		return 0, err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()
	var imported int64
	for _, proxy := range proxies {
		result, err := transaction.ExecContext(ctx, `INSERT INTO proxies (id, url, label, created_at) VALUES (?,?,?,?) ON CONFLICT(id) DO NOTHING`, proxy.ID, proxy.URL, proxy.Label, time.Now().Unix())
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		imported += count
	}
	return imported, transaction.Commit()
}

func (s *Store) Proxies(ctx context.Context) ([]outbound.Proxy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, label FROM proxies ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	proxies := make([]outbound.Proxy, 0)
	for rows.Next() {
		var proxy outbound.Proxy
		if err := rows.Scan(&proxy.ID, &proxy.Label); err != nil {
			return nil, err
		}
		proxies = append(proxies, proxy)
	}
	return proxies, rows.Err()
}

func (s *Store) ProxyURL(ctx context.Context, id string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT url FROM proxies WHERE id=?`, id).Scan(&value)
	return value, err
}

func (s *Store) DeleteProxy(ctx context.Context, id string) (bool, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	var bound int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE proxy_id=?`, id).Scan(&bound); err != nil {
		return false, err
	}
	if bound > 0 {
		return false, ErrProxyInUse
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM proxies WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count > 0, transaction.Commit()
}

func (s *Store) SetProxy(ctx context.Context, id int64, proxyID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE accounts SET proxy_id=?, updated_at=? WHERE id=? AND (? IN ('', 'direct') OR EXISTS (SELECT 1 FROM proxies WHERE id=?))`, proxyID, time.Now().Unix(), id, proxyID, proxyID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
