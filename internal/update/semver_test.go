package update

import "testing"

func TestParseVersion(t *testing.T) {
	good := map[string]string{
		"1.2.3":             "1.2.3",
		"v1.2.3":            "1.2.3",
		" v1.2.3 ":          "1.2.3",
		"0.0.0":             "0.0.0",
		"1.2.3-rc.1":        "1.2.3-rc.1",
		"v1.2.3-rc.1":       "1.2.3-rc.1",
		"1.2.3-alpha":       "1.2.3-alpha",
		"1.2.3-x-y.0":       "1.2.3-x-y.0",
		"1.2.3+build.5":     "1.2.3+build.5",
		"1.2.3-rc.1+b.7":    "1.2.3-rc.1+b.7",
		"10.20.30":          "10.20.30",
		"999999999.0.0":     "999999999.0.0",
		"1.2.3-0A.is.legal": "1.2.3-0A.is.legal",
	}
	for in, want := range good {
		v, err := ParseVersion(in)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", in, err)
			continue
		}
		if v.String() != want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", in, v.String(), want)
		}
	}
	bad := []string{
		"", "dev", "v", "1", "1.2", "1.2.3.4", "a.b.c", "1.2.x", "1.2.-3", "1..3",
		"1.2.3-", "1.2.3-rc..1", "1.2.3-01", "1.2.3+", "1.2.3 rc", "latest", "vv1.2.3",
		"1.2.3-rc_1", "9999999999.0.0", "1.2.3-123456789012345678901",
	}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded, want error", in)
		}
	}
}

func TestCompareOrdering(t *testing.T) {
	// Strictly ascending, including the semver.org pre-release example.
	asc := []string{
		"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
		"2.0.1", "10.0.0",
	}
	for i := range asc {
		for j := range asc {
			got, ok := CompareVersions(asc[i], asc[j])
			if !ok {
				t.Fatalf("CompareVersions(%q, %q) not ok", asc[i], asc[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", asc[i], asc[j], got, want)
			}
		}
	}
}

func TestCompareDetails(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3+a", "1.2.3+b", 0}, // build metadata ignored
		{"1.2.3-rc.1", "1.2.3", -1},
		{"1.2.10", "1.2.9", 1}, // numeric, not lexical
		{"1.10.0", "1.9.0", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-1", "1.0.0-a", -1}, // numeric < alphanumeric
		{"1.0.0-rc", "1.0.0-rc.1", -1},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if !ok || got != c.want {
			t.Errorf("Compare(%q, %q) = %d (ok=%v), want %d", c.a, c.b, got, ok, c.want)
		}
	}
	if _, ok := CompareVersions("dev", "1.0.0"); ok {
		t.Error("dev must not be comparable")
	}
	if _, ok := CompareVersions("1.0.0", "garbage"); ok {
		t.Error("garbage must not be comparable")
	}
}

func TestNormalize(t *testing.T) {
	if Normalize("v1.2.3") != "1.2.3" || Normalize(" 1.2.3 ") != "1.2.3" || Normalize("dev") != "dev" {
		t.Fatal("Normalize wrong")
	}
}
