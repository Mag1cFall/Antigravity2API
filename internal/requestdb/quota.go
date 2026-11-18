package requestdb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// QuotaSnapshots 按时间升序返回范围内的额度快照，account 为空时不限账户
func (store *Store) QuotaSnapshots(ctx context.Context, from, to time.Time, account string) ([]QuotaSnapshot, error) {
	query := `SELECT time, account, bucket_id, window, remaining_fraction, reset_time FROM quota_snapshots WHERE time >= ? AND time < ?`
	args := []any{from.UnixMilli(), to.UnixMilli()}
	if account != "" {
		query += ` AND account = ?`
		args = append(args, account)
	}
	return store.quotaSnapshots(ctx, query+` ORDER BY time, rowid`, args...)
}

// LatestQuotaSnapshots 返回账户在 since 之后最近一次额度读取的全部快照
func (store *Store) LatestQuotaSnapshots(ctx context.Context, account string, since time.Time) ([]QuotaSnapshot, error) {
	return store.quotaSnapshots(ctx, `SELECT time, account, bucket_id, window, remaining_fraction, reset_time FROM quota_snapshots
WHERE account = ? AND time = (SELECT MAX(time) FROM quota_snapshots WHERE account = ? AND time >= ?) ORDER BY rowid`, account, account, since.UnixMilli())
}

// quotaSnapshots 执行额度快照查询并读出各行
func (store *Store) quotaSnapshots(ctx context.Context, query string, args ...any) ([]QuotaSnapshot, error) {
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	snapshots := []QuotaSnapshot{}
	for rows.Next() {
		var snapshot QuotaSnapshot
		var at int64
		var reset sql.NullInt64
		if err := rows.Scan(&at, &snapshot.Account, &snapshot.BucketID, &snapshot.Window, &snapshot.RemainingFraction, &reset); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		snapshot.Time = time.UnixMilli(at).UTC()
		if reset.Valid {
			resetTime := time.UnixMilli(reset.Int64).UTC()
			snapshot.ResetTime = &resetTime
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, errors.Join(rows.Err(), rows.Close())
}
