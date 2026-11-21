package main

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		current string
		latest  string
		want    int
	}{
		{"0.0.2", "v0.0.3", -1},
		{"0.0.3", "0.0.3", 0},
		{"0.0.4", "0.0.3", 1},
		{"0.0.3+dirty", "0.0.3", 0},
		{"0.0.0-dev", "0.0.3", -1},
		{"1.2.3", "1.10.0", -1},
	}
	for _, test := range cases {
		got, err := compareVersions(test.current, test.latest)
		if err != nil {
			t.Fatalf("compare %q and %q: %v", test.current, test.latest, err)
		}
		if got != test.want {
			t.Fatalf("compare %q and %q = %d, want %d", test.current, test.latest, got, test.want)
		}
	}
}

func TestCompareVersionsRejectsMalformedValues(t *testing.T) {
	for _, values := range [][2]string{{"0.0", "0.0.3"}, {"0.0.3", "latest"}} {
		if _, err := compareVersions(values[0], values[1]); err == nil {
			t.Fatalf("compare %q and %q accepted a malformed version", values[0], values[1])
		}
	}
}

func TestVersionsEqualIgnoresReleasePrefixAndBuildSuffix(t *testing.T) {
	if !versionsEqual("v0.0.3", "0.0.3+dirty") {
		t.Fatal("a release tag and a dirty build of the same version should compare equal")
	}
	if versionsEqual("0.0.2", "0.0.3") {
		t.Fatal("different versions compared equal")
	}
}
