package api

import "testing"

func TestParseSubnetReturnsTheNetwork(t *testing.T) {
	n, err := ParseSubnet("10.0.0.5/24")
	if err != nil || n.String() != "10.0.0.0/24" {
		t.Fatalf("ParseSubnet = %v, %v; want 10.0.0.0/24", n, err)
	}
	for _, bad := range []string{"", "abc", "10.0.0.0", "10.0.0.0/31", "300.0.0.0/24", "fd00::/64"} {
		if _, err := ParseSubnet(bad); err == nil {
			t.Errorf("ParseSubnet(%q) should fail", bad)
		}
	}
}

func TestSubnetsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.0.0.0/24", "10.0.0.0/24", true},
		{"10.0.0.0/16", "10.0.5.0/24", true},
		{"10.0.5.0/24", "10.0.0.0/16", true},
		{"10.0.0.0/25", "10.0.0.128/25", false},
		{"10.0.0.0/24", "10.1.0.0/24", false},
		{"10.0.0.0/24", "garbage", false},
	}
	for _, c := range cases {
		if got := SubnetsOverlap(c.a, c.b); got != c.want {
			t.Errorf("SubnetsOverlap(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
