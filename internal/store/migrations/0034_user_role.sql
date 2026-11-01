-- Third role: admin > user > viewer. The original users table limited role to
-- ('admin','viewer','disabled'), which rejects the new 'user' role. Roles are
-- validated in Go (internal/auth/roles.go), so the CHECK only keeps the value
-- to a known set. The table is rebuilt (the runner turns foreign keys off, so
-- sessions, local_credentials and every created_by/author_id link survive);
-- ids and existing roles are preserved, nobody is promoted or demoted.
CREATE TABLE users_new (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','user','viewer','disabled')),
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);
INSERT INTO users_new (id, email, name, avatar_url, role, created_at, last_login_at)
  SELECT id, email, name, avatar_url, role, created_at, last_login_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
