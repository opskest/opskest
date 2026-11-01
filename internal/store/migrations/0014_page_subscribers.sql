-- Status page subscribers (email with double opt-in, or a Slack incoming
-- webhook), notified of every public incident update on the page.
ALTER TABLE status_pages ADD COLUMN subscriptions_enabled INTEGER NOT NULL DEFAULT 0;
-- The email channel whose SMTP server sends subscriber mail.
ALTER TABLE status_pages ADD COLUMN subscriber_channel_id INTEGER REFERENCES channels(id) ON DELETE SET NULL;

CREATE TABLE page_subscribers (
  id INTEGER PRIMARY KEY,
  page_id INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('email', 'slack')),
  email TEXT,
  slack_enc BLOB,
  -- sha256 of the webhook URL: the ciphertext is randomised, so this is
  -- what makes one webhook subscribable once per page.
  slack_hash TEXT,
  token TEXT NOT NULL UNIQUE,
  confirmed_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX page_subscribers_email ON page_subscribers(page_id, email) WHERE kind = 'email';
CREATE UNIQUE INDEX page_subscribers_slack ON page_subscribers(page_id, slack_hash) WHERE kind = 'slack';
CREATE INDEX page_subscribers_page ON page_subscribers(page_id, confirmed_at);
