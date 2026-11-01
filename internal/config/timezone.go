package config

import (
	"fmt"
	"time"
)

// Explicit application configuration wins, followed by the process/host zone.
// UTC is the portable fallback for hosts which expose no IANA zone information.
func displayLocation(explicit, processZone string, hostZone func() string) (*time.Location, error) {
	if explicit != "" {
		if explicit == "Local" {
			return nil, fmt.Errorf("use an IANA timezone or leave empty to inherit the server timezone")
		}
		return time.LoadLocation(explicit)
	}
	if processZone != "" {
		if processZone == "Local" {
			return nil, fmt.Errorf("TZ must be an IANA timezone, not Local")
		}
		return time.LoadLocation(processZone)
	}
	if name := hostZone(); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	return time.UTC, nil
}
