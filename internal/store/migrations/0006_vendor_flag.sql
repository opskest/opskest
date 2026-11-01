-- 3rd-party status feeds become an explicit per-monitor flag
-- (settings.vendor) instead of being guessed from the URL on every check.
-- Mark the monitors the old guess treated as vendors: known vendor status
-- hosts, or anything in a 3rd-party / vendor group.
UPDATE monitors
SET settings_json = json_set(CASE WHEN json_valid(settings_json) THEN settings_json ELSE '{}' END, '$.vendor', json('true'))
WHERE type = 'http' AND (
  target LIKE '%metastatus.com%' OR target LIKE '%githubstatus.com%' OR target LIKE '%statuspage.io%' OR
  target LIKE '%cloudflarestatus.com%' OR target LIKE '%status.firebase.google.com%' OR
  target LIKE '%status.cloud.google.com%' OR target LIKE '%google.com/appsstatus%' OR
  target LIKE '%status.slack.com%' OR target LIKE '%status.openai.com%' OR target LIKE '%status.stripe.com%' OR
  group_id IN (SELECT id FROM groups WHERE lower(name) LIKE '%3rd%' OR lower(name) LIKE '%third%party%' OR lower(name) LIKE '%vendor%')
);
