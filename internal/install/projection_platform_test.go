package install

import "testing"

func TestIsWSLKernel(t *testing.T) {
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"5.15.153.1-microsoft-standard-WSL2", true},
		{"Linux version 4.4.0-19041-Microsoft", true},
		{"6.8.0-generic", false},
	} {
		if got := isWSLKernel(test.version); got != test.want {
			t.Fatalf("isWSLKernel(%q) = %v, want %v", test.version, got, test.want)
		}
	}
}
