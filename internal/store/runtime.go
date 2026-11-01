package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Runtime is one monitor's monitor_runtime row: certificate and domain
// expiry state that must survive restarts.
type Runtime struct {
	MonitorID int64

	CertNotAfter    *int64
	CertIssuer      string
	CertSubject     string
	CertAlertedDays *int

	DomainExpiresAt   *int64
	DomainAlertedDays *int
	DomainCheckedAt   *int64

	LastPushAt *int64
}

func intPtrOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func scanIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// AllRuntime returns every monitor's runtime row, keyed by monitor id,
// for the boot-time load (spec §5.4).
func (s *Store) AllRuntime(ctx context.Context) (map[int64]Runtime, error) {
	rows, err := s.R.QueryContext(ctx, `
		SELECT monitor_id, cert_not_after, cert_issuer, cert_subject, cert_alerted_days,
			domain_expires_at, domain_alerted_days, domain_checked_at, last_push_at
		FROM monitor_runtime`)
	if err != nil {
		return nil, fmt.Errorf("store: all runtime: %w", err)
	}
	defer rows.Close()

	out := map[int64]Runtime{}
	for rows.Next() {
		var r Runtime
		var certNotAfter, domainExpiresAt, domainCheckedAt, lastPushAt sql.NullInt64
		var certAlertedDays, domainAlertedDays sql.NullInt64
		var certIssuer, certSubject sql.NullString

		if err := rows.Scan(
			&r.MonitorID, &certNotAfter, &certIssuer, &certSubject, &certAlertedDays,
			&domainExpiresAt, &domainAlertedDays, &domainCheckedAt, &lastPushAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan runtime: %w", err)
		}

		r.CertNotAfter = scanNullInt64(certNotAfter)
		r.CertIssuer = certIssuer.String
		r.CertSubject = certSubject.String
		r.CertAlertedDays = scanIntPtr(certAlertedDays)
		r.DomainExpiresAt = scanNullInt64(domainExpiresAt)
		r.DomainAlertedDays = scanIntPtr(domainAlertedDays)
		r.DomainCheckedAt = scanNullInt64(domainCheckedAt)
		r.LastPushAt = scanNullInt64(lastPushAt)

		out[r.MonitorID] = r
	}
	return out, rows.Err()
}

// OpUpsertRuntime inserts or replaces monitorID's runtime row, for use
// with Writer.Do.
func OpUpsertRuntime(r Runtime) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO monitor_runtime(
				monitor_id, cert_not_after, cert_issuer, cert_subject, cert_alerted_days,
				domain_expires_at, domain_alerted_days, domain_checked_at, last_push_at
			) VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(monitor_id) DO UPDATE SET
				cert_not_after=excluded.cert_not_after,
				cert_issuer=excluded.cert_issuer,
				cert_subject=excluded.cert_subject,
				cert_alerted_days=excluded.cert_alerted_days,
				domain_expires_at=excluded.domain_expires_at,
				domain_alerted_days=excluded.domain_alerted_days,
				domain_checked_at=excluded.domain_checked_at,
				last_push_at=excluded.last_push_at`,
			r.MonitorID, int64OrNil(r.CertNotAfter), nullString(r.CertIssuer), nullString(r.CertSubject),
			intPtrOrNil(r.CertAlertedDays), int64OrNil(r.DomainExpiresAt), intPtrOrNil(r.DomainAlertedDays),
			int64OrNil(r.DomainCheckedAt), int64OrNil(r.LastPushAt),
		)
		return err
	}
}
