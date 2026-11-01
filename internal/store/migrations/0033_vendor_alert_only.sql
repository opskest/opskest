-- 3rd-party feeds (monitors.settings.vendor = true) are alert-only: they are
-- never outages, uptime, incident links, status-page components, dependency
-- parents/children or maintenance targets. Clean up what earlier versions
-- recorded for them. Idempotent: every statement only removes rows that
-- belong to a vendor monitor, so a re-run finds nothing.
CREATE TEMP TABLE _vendor_ids AS
  SELECT id FROM monitors
  WHERE CASE WHEN json_valid(settings_json) THEN json_extract(settings_json, '$.vendor') END = 1;

DELETE FROM outage_notes WHERE outage_id IN (SELECT id FROM outages WHERE monitor_id IN (SELECT id FROM _vendor_ids));
DELETE FROM outages WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM region_results WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM rollup_hourly WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM rollup_daily WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM incident_monitors WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM maintenance_targets WHERE monitor_id IN (SELECT id FROM _vendor_ids);
DELETE FROM status_page_items WHERE monitor_id IN (SELECT id FROM _vendor_ids);

-- A vendor monitor depends on nothing; nothing depends on it or contains it.
UPDATE monitors
SET settings_json = json_remove(settings_json, '$.dependsOn', '$.children')
WHERE id IN (SELECT id FROM _vendor_ids)
  AND (json_type(settings_json, '$.dependsOn') IS NOT NULL OR json_type(settings_json, '$.children') IS NOT NULL);

UPDATE monitors
SET settings_json = CASE
  WHEN (SELECT count(*) FROM json_each(monitors.settings_json, '$.dependsOn') WHERE value NOT IN (SELECT id FROM _vendor_ids)) = 0
    THEN json_remove(monitors.settings_json, '$.dependsOn')
  ELSE json_set(monitors.settings_json, '$.dependsOn',
    (SELECT json(json_group_array(value)) FROM json_each(monitors.settings_json, '$.dependsOn') WHERE value NOT IN (SELECT id FROM _vendor_ids)))
  END
WHERE json_valid(settings_json) AND json_type(settings_json, '$.dependsOn') = 'array'
  AND EXISTS (SELECT 1 FROM json_each(monitors.settings_json, '$.dependsOn') WHERE value IN (SELECT id FROM _vendor_ids));

UPDATE monitors
SET settings_json = CASE
  WHEN (SELECT count(*) FROM json_each(monitors.settings_json, '$.children') WHERE value NOT IN (SELECT id FROM _vendor_ids)) = 0
    THEN json_remove(monitors.settings_json, '$.children')
  ELSE json_set(monitors.settings_json, '$.children',
    (SELECT json(json_group_array(value)) FROM json_each(monitors.settings_json, '$.children') WHERE value NOT IN (SELECT id FROM _vendor_ids)))
  END
WHERE json_valid(settings_json) AND json_type(settings_json, '$.children') = 'array'
  AND EXISTS (SELECT 1 FROM json_each(monitors.settings_json, '$.children') WHERE value IN (SELECT id FROM _vendor_ids));

DROP TABLE _vendor_ids;
