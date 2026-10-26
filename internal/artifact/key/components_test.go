package key

import "testing"

func TestHasComponents(t *testing.T) {
	tests := []struct {
		name, dep string
		want      bool
	}{
		{"grch38/star/2.7.11b/gencode49", "star/2.7.11b", true},
		{"grch38/star/2.7.11b/gencode49", "grch38/star", true},
		{"grch38/star/2.7.11b/gencode49", "gencode49", true},
		// Substring matching is never used.
		{"grch38/star2.7.11b/gencode49", "star/2.7.11b", false},
		{"grch38/star/2.7.11b-old/gencode49", "star/2.7.11b", false},
		// Versions are strings, never compared semantically.
		{"grch38/star/2.7.11/gencode49", "star/2.7.11b", false},
		// The run has to be contiguous, and an OS name carries its distro.
		{"grch38/star/gencode49/2.7.11b", "star/2.7.11b", false},
		{"grch38/pytorch/2.9/index", "ubuntu24/pytorch/2.9", false},
		{"grch38/ubuntu24/pytorch/2.9", "ubuntu24/pytorch/2.9", true},
		{"star/2.7.11b", "star/2.7.11b", true},
		{"star", "star/2.7.11b", false},
	}
	for _, tt := range tests {
		if got := hasComponents(tt.name, tt.dep); got != tt.want {
			t.Errorf("hasComponents(%q, %q) = %v, want %v", tt.name, tt.dep, got, tt.want)
		}
	}
}
