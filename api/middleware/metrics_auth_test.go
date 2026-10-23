package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Prescott-Data/athena/internal/config"
	"github.com/gin-gonic/gin"
)

func metricsStatus(t *testing.T, cfg *config.AuthConfig, path, key string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NewAuthMiddleware(cfg).GinAuthMiddleware())
	r.GET(path, func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestMetricsNeedAServiceKey(t *testing.T) {
	cfg := &config.AuthConfig{RequireAPIKey: true, APIKeys: map[string]string{"tenant-key": "acme", "svc-key": config.ServiceTenant}}
	for _, tt := range []struct {
		name, key string
		want      int
	}{
		{"no key", "", http.StatusUnauthorized},
		{"unknown key", "wrong", http.StatusUnauthorized},
		{"tenant key", "tenant-key", http.StatusForbidden},
		{"service key", "svc-key", http.StatusOK},
	} {
		if got := metricsStatus(t, cfg, "/metrics", tt.key); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
	if got := metricsStatus(t, cfg, "/health", ""); got != http.StatusOK {
		t.Errorf("/health without a key: got %d, want 200", got)
	}
}

func TestMetricsOptOuts(t *testing.T) {
	public := &config.AuthConfig{RequireAPIKey: true, APIKeys: map[string]string{"k": "acme"}, MetricsPublic: true}
	if got := metricsStatus(t, public, "/metrics", ""); got != http.StatusOK {
		t.Errorf("MetricsPublic: got %d, want 200", got)
	}
	unauthenticated := &config.AuthConfig{AllowUnauthenticated: true}
	if got := metricsStatus(t, unauthenticated, "/metrics", ""); got != http.StatusOK {
		t.Errorf("authentication off: got %d, want 200", got)
	}
}
