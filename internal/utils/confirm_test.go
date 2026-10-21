package utils

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestConfirmDefaultYes(t *testing.T) {
	for _, tc := range []struct {
		why, input string
		want       bool
	}{
		{"Enter agrees", "\n", true},
		{"y agrees", "y\n", true},
		{"yes agrees in any case", "YES\n", true},
		{"n declines", "n\n", false},
		{"anything else declines", "maybe\n", false},
		{"input that ended declines", "", false},
	} {
		var out bytes.Buffer
		got := confirmDefaultYes(context.Background(), &out, "Push? [Y/n]: ", bufio.NewReader(strings.NewReader(tc.input)))
		if got != tc.want {
			t.Errorf("%s: answered %v, want %v", tc.why, got, tc.want)
		}
	}
}
