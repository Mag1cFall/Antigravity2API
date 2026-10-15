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
	rows, err := store.db.QueryContext(ctx, query+` ORDER BY time, rowid`, args...)
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
