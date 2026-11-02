package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Settings holds every value editable in Settings -> General.
type Settings struct {
	RetentionRawDays   int `json:"retentionRawDays"`
	StormThreshold     int `json:"stormThreshold"`
	StormWindowS       int `json:"stormWindowS"`
	BackupHour         int `json:"backupHour"` // Hour of day (0-23) in the configured display timezone.
	BackupKeep         int `json:"backupKeep"`
	DefaultReminderMin int `json:"defaultReminderMin"`
	// RequireTwoFactor: every account must set up an authenticator app
	// before it can use Opskest.
	RequireTwoFactor bool `json:"requireTwoFactor"`
	// RegionConfirm (0/1): confirm new outages from other regions via
	// Globalping. Off by default — it sends monitored URLs to a third party.
	RegionConfirm int `json:"regionConfirm"`
	// RegionCountries are the worldwide locations (ISO codes, one probe
	// each) for monitors set to Worldwide, and for confirming outages.
	RegionCountries []string `json:"regionCountries"`
	// RegionSweep (0/1): check every Worldwide monitor from those
	// countries on a rotation. RegionTestsPerHour is the Globalping
	// allowance to pace against (one test = one probe; free: 250, or 500
	// with a token). RegionAlerts (0/1): alert when some countries fail.
	RegionSweep        int `json:"regionSweep"`
	RegionTestsPerHour int `json:"regionTestsPerHour"`
	RegionAlerts       int `json:"regionAlerts"`
}

// UnmarshalJSON accepts the legacy "backupHourIst" key written by older
// clients as an alias of "backupHour" (the new key wins when both are sent).
// Unknown fields are still rejected.
func (v *Settings) UnmarshalJSON(data []byte) error {
	type plain Settings
	aux := struct {
		*plain
		BackupHour       *int `json:"backupHour"` // shadows the embedded field
		LegacyBackupHour *int `json:"backupHourIst"`
	}{plain: (*plain)(v)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return err
	}
	switch {
	case aux.BackupHour != nil:
		v.BackupHour = *aux.BackupHour
	case aux.LegacyBackupHour != nil:
		v.BackupHour = *aux.LegacyBackupHour
	}
	return nil
}

// DefaultRegionCountries provides a geographically distributed probe set.
// The host's own primary checks always run from wherever it is deployed.
var DefaultRegionCountries = []string{"US", "CA", "BR", "GB", "DE", "FR", "NL", "RU", "ZA", "NG", "AE", "IN", "JP", "KR", "CN", "SG", "AU"}

// MaxRegionCountries caps the worldwide list.
const MaxRegionCountries = 25

// DefaultSettings returns the values used for any key missing from the
// settings table.
func DefaultSettings() Settings {
	return Settings{
		RetentionRawDays:   7,
		StormThreshold:     5,
		StormWindowS:       30,
		BackupHour:         2,
		BackupKeep:         7,
		DefaultReminderMin: 0,
		RegionCountries:    append([]string(nil), DefaultRegionCountries...),
		RegionSweep:        1,
		RegionTestsPerHour: 225,
		RegionAlerts:       1,
	}
}

const (
	keyRetentionRawDays = "retention_raw_days"
	keyStormThreshold   = "storm_threshold"
	keyStormWindowS     = "storm_window_s"
	keyBackupHour       = "backup_hour"
	// keyBackupHourLegacy is the pre-rename settings key. It is still read
	// when keyBackupHour is absent so existing databases keep their schedule.
	keyBackupHourLegacy   = "backup_hour_ist"
	keyBackupKeep         = "backup_keep"
	keyDefaultReminderMin = "default_reminder_min"
	keyRegionConfirm      = "region_confirm"
	keyRegionCountries    = "region_countries"
	keyRegionSweep        = "region_sweep"
	keyRegionTestsPerHour = "region_tests_per_hour"
	keyRegionAlerts       = "region_alerts"
	keyRequireTwoFactor   = "require_two_factor"
)

// GetSettings reads the settings table, filling in DefaultSettings() for
// any key that is missing or fails to parse as an integer.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	v := DefaultSettings()

	rows, err := s.R.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return v, fmt.Errorf("store: get settings: %w", err)
	}
	defer rows.Close()

	raw := map[string]string{}
	for rows.Next() {
		var k, val string
		if err := rows.Scan(&k, &val); err != nil {
			return v, fmt.Errorf("store: scan setting: %w", err)
		}
		raw[k] = val
	}
	if err := rows.Err(); err != nil {
		return v, err
	}

	v.RequireTwoFactor = raw[keyRequireTwoFactor] == "1"
	if n, ok := parseIntSetting(raw, keyRetentionRawDays); ok {
		v.RetentionRawDays = n
	}
	if n, ok := parseIntSetting(raw, keyStormThreshold); ok {
		v.StormThreshold = n
	}
	if n, ok := parseIntSetting(raw, keyStormWindowS); ok {
		v.StormWindowS = n
	}
	if n, ok := parseIntSetting(raw, keyBackupHour); ok {
		v.BackupHour = n
	} else if n, ok := parseIntSetting(raw, keyBackupHourLegacy); ok {
		v.BackupHour = n
	}
	if n, ok := parseIntSetting(raw, keyBackupKeep); ok {
		v.BackupKeep = n
	}
	if n, ok := parseIntSetting(raw, keyDefaultReminderMin); ok {
		v.DefaultReminderMin = n
	}
	if n, ok := parseIntSetting(raw, keyRegionConfirm); ok {
		v.RegionConfirm = n
	}
	if n, ok := parseIntSetting(raw, keyRegionSweep); ok {
		v.RegionSweep = n
	}
	if n, ok := parseIntSetting(raw, keyRegionTestsPerHour); ok {
		v.RegionTestsPerHour = n
	}
	if n, ok := parseIntSetting(raw, keyRegionAlerts); ok {
		v.RegionAlerts = n
	}
	if c := strings.TrimSpace(raw[keyRegionCountries]); c != "" {
		v.RegionCountries = strings.Split(c, ",")
	}
	return v, nil
}

func parseIntSetting(raw map[string]string, key string) (int, bool) {
	val, ok := raw[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, false
	}
	return n, true
}

// validate checks the ranges the UI and this store enforce for every
// setting.
func (v Settings) validate() error {
	if v.RetentionRawDays < 1 || v.RetentionRawDays > 90 {
		return fmt.Errorf("store: retentionRawDays must be between 1 and 90, got %d", v.RetentionRawDays)
	}
	if v.StormThreshold < 2 || v.StormThreshold > 100 {
		return fmt.Errorf("store: stormThreshold must be between 2 and 100, got %d", v.StormThreshold)
	}
	if v.StormWindowS < 5 || v.StormWindowS > 600 {
		return fmt.Errorf("store: stormWindowS must be between 5 and 600, got %d", v.StormWindowS)
	}
	if v.BackupHour < 0 || v.BackupHour > 23 {
		return fmt.Errorf("store: backup hour must be between 0 and 23, got %d", v.BackupHour)
	}
	if v.BackupKeep < 1 || v.BackupKeep > 60 {
		return fmt.Errorf("store: backupKeep must be between 1 and 60, got %d", v.BackupKeep)
	}
	if v.DefaultReminderMin < 0 || v.DefaultReminderMin > 1440 {
		return fmt.Errorf("store: defaultReminderMin must be between 0 and 1440, got %d", v.DefaultReminderMin)
	}
	if len(v.RegionCountries) < 1 || len(v.RegionCountries) > MaxRegionCountries {
		return fmt.Errorf("store: pick 1 to %d regionCountries, got %d", MaxRegionCountries, len(v.RegionCountries))
	}
	for _, c := range v.RegionCountries {
		if len(c) != 2 || strings.ToUpper(c) != c || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
			return fmt.Errorf("store: regionCountries must be 2-letter country codes, got %q", c)
		}
	}
	if v.RegionTestsPerHour < 10 || v.RegionTestsPerHour > 100000 {
		return fmt.Errorf("store: regionTestsPerHour must be between 10 and 100000, got %d", v.RegionTestsPerHour)
	}
	if v.RegionSweep != 0 && v.RegionSweep != 1 || v.RegionAlerts != 0 && v.RegionAlerts != 1 {
		return fmt.Errorf("store: regionSweep and regionAlerts must be 0 or 1")
	}
	if v.RegionConfirm != 0 && v.RegionConfirm != 1 {
		return fmt.Errorf("store: regionConfirm must be 0 or 1, got %d", v.RegionConfirm)
	}
	return nil
}

// PutSettings validates v and upserts every key in one transaction.
func (s *Store) PutSettings(ctx context.Context, v Settings) error {
	// Older clients (and the General form) may not send the region fields.
	if len(v.RegionCountries) == 0 {
		v.RegionCountries = append([]string(nil), DefaultRegionCountries...)
	}
	for i, c := range v.RegionCountries {
		v.RegionCountries[i] = strings.ToUpper(strings.TrimSpace(c))
	}
	if v.RegionTestsPerHour == 0 {
		v.RegionTestsPerHour = 225
	}
	if err := v.validate(); err != nil {
		return err
	}

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	kv := map[string]int{
		keyRetentionRawDays:   v.RetentionRawDays,
		keyStormThreshold:     v.StormThreshold,
		keyStormWindowS:       v.StormWindowS,
		keyBackupHour:         v.BackupHour,
		keyBackupKeep:         v.BackupKeep,
		keyDefaultReminderMin: v.DefaultReminderMin,
		keyRegionConfirm:      v.RegionConfirm,
		keyRegionSweep:        v.RegionSweep,
		keyRegionTestsPerHour: v.RegionTestsPerHour,
		keyRegionAlerts:       v.RegionAlerts,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, keyRegionCountries, strings.Join(v.RegionCountries, ","),
	); err != nil {
		return fmt.Errorf("store: put setting %s: %w", keyRegionCountries, err)
	}
	if v.RequireTwoFactor {
		kv[keyRequireTwoFactor] = 1
	} else {
		kv[keyRequireTwoFactor] = 0
	}
	for k, n := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, strconv.Itoa(n),
		); err != nil {
			return fmt.Errorf("store: put setting %s: %w", k, err)
		}
	}
	// The legacy key is superseded by keyBackupHour; drop it so the two can
	// never disagree.
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, keyBackupHourLegacy); err != nil {
		return fmt.Errorf("store: drop legacy setting %s: %w", keyBackupHourLegacy, err)
	}
	return tx.Commit()
}
