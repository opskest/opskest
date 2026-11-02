-- An outage opened while one of the monitor's parents (Settings.DependsOn)
-- was down: recorded, but its alerts are held. NULL once released or for
-- ordinary outages.
ALTER TABLE outages ADD COLUMN suppressed_by INTEGER;
