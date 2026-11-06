package model

import "testing"

func TestHumanBytes(t *testing.T) {
	tests := map[uint64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KB",
		1536:        "1.5 KB",
		157286400:   "150 MB",
		3650722201:  "3.4 GB",
		19327352832: "18.0 GB",
	}
	for in, want := range tests {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanRate(t *testing.T) {
	tests := map[float64]string{0: "0 B/s", 0.4: "0 B/s", 512: "512 B/s", 839680: "820 KB/s"}
	for in, want := range tests {
		if got := HumanRate(in); got != want {
			t.Errorf("HumanRate(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanCount(t *testing.T) {
	tests := map[uint64]string{0: "0", 950: "950", 9999: "9999", 12345: "12K", 999_999: "999K", 1_234_567: "1.2M", 2_431_839: "2.4M"}
	for in, want := range tests {
		if got := HumanCount(in); got != want {
			t.Errorf("HumanCount(%d) = %q, want %q", in, got, want)
		}
	}
}
