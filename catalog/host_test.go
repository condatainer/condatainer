package catalog

import (
	"net/http"
	"testing"
)

// A known host gets its own entry; any other is read flat with a Bearer token.
func TestHostFor(t *testing.T) {
	tests := []struct{ base, name, url string }{
		{"https://raw.githubusercontent.com/lab/recipes/main", "github", "https://raw.githubusercontent.com/lab/recipes/main/index/recipes.json"},
		{"https://RAW.githubusercontent.com/lab/recipes/main/", "github", "https://RAW.githubusercontent.com/lab/recipes/main/index/recipes.json"},
		{"https://recipes.example.org/lab", "http", "https://recipes.example.org/lab/index/recipes.json"},
	}
	for _, tt := range tests {
		api := hostFor(tt.base)
		if api.name != tt.name {
			t.Errorf("%s: host %q, want %q", tt.base, api.name, tt.name)
		}
		if got := api.fileURL(tt.base, "index/recipes.json"); got != tt.url {
			t.Errorf("%s: url %s, want %s", tt.base, got, tt.url)
		}
		req, _ := http.NewRequest(http.MethodGet, tt.url, nil)
		api.authorize(req, "t0ken")
		if got := req.Header.Get("Authorization"); got != "Bearer t0ken" {
			t.Errorf("%s: Authorization = %q", tt.base, got)
		}
	}
}
