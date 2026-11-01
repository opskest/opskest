-- Local accounts are separate from users so sessions, roles and audit IDs stay stable.
CREATE TABLE local_credentials (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  totp_secret BLOB,
  totp_pending BLOB,
  pending_expires INTEGER NOT NULL DEFAULT 0,
  last_totp_step INTEGER NOT NULL DEFAULT -1,
  recovery_hashes TEXT NOT NULL DEFAULT '[]'
);
