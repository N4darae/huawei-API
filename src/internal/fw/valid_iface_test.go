package fw

import "testing"

func TestValidIface(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"enode0", true},
		{"lo", true},
		{"eth0", true},
		{"wwan0", true},
		{"dongle-1", true},
		{"dongle-02", true},
		{"a.b", true},
		{"", false},
		{"too-long-name-exceeding-15-bytes", false},
		{"bad;name", false},
		{"bad}name", false},
		{"bad\nname", false},
		{"bad name", false},
		{".", false},
		{"..", false},
		{"a/b", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validIface(c.name)
			if c.ok && err != nil {
				t.Errorf("validIface(%q) = %v, want nil", c.name, err)
			}
			if !c.ok && err == nil {
				t.Errorf("validIface(%q) = nil, want error", c.name)
			}
		})
	}
}
