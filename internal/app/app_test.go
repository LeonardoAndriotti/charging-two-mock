package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type memoryStore struct {
	mu       sync.Mutex
	value    string
	conflict bool
}

func (s *memoryStore) Load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, nil
}
func (s *memoryStore) CompareAndSwap(_ context.Context, old, next string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conflict {
		s.conflict = false
		return false, nil
	}
	if s.value != old {
		return false, nil
	}
	s.value = next
	return true, nil
}
func request(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))

	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestIndependentInstancesShareIdempotency(t *testing.T) {
	store := &memoryStore{conflict: true}
	one := New("wallet", store)
	two := New("wallet", store)
	first := request(one, "POST", "/v1/wallets", "create", "{}")
	if first.Code != 201 {
		t.Fatal(first.Body.String())
	}
	second := request(two, "POST", "/v1/wallets", "create", "{}")
	if second.Body.String() != first.Body.String() {
		t.Fatalf("different responses: %s %s", first.Body, second.Body)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &wallet)
	if request(two, "GET", "/v1/wallets/"+wallet.ID, "", "").Code != 200 {
		t.Fatal("lost state")
	}
	if request(two, "POST", "/v1/wallets", "create", `{"amount_cents":1}`).Code != 409 {
		t.Fatal("lost fingerprint")
	}
}
func TestConfiguration(t *testing.T) {
	t.Setenv("KV_REST_API_URL", "")
	t.Setenv("KV_REST_API_TOKEN", "")
	t.Setenv("VERCEL", "")
	t.Setenv("UPSTASH_REDIS_REST_URL", "")
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "")
	if _, err := FromEnv("wallet"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VERCEL", "1")
	t.Setenv("UPSTASH_REDIS_REST_URL", "")
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "")
	if _, err := FromEnv("wallet"); err == nil {
		t.Fatal("memory accepted on Vercel")
	}
}

func TestPublicEndpointsWithoutAuthentication(t *testing.T) {
	for _, profile := range []string{"wallet", "charging-ocpi", "charging-rest"} {
		t.Run(profile, func(t *testing.T) {
			h := New(profile, nil)
			for _, path := range []string{"/healthz", "/v1/capabilities"} {
				if w := request(h, "GET", path, "", ""); w.Code != 200 {
					t.Fatalf("%s: %d", path, w.Code)
				}
			}
			path, body, want := "/v1/sessions", `{"connector_id":"connector-1"}`, 202
			if profile == "wallet" {
				path, body, want = "/v1/wallets", "{}", 201
			}
			if w := request(h, "POST", path, "public-create", body); w.Code != want {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestRedisMarketplaceConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, upURL, upToken, kvURL, kvToken, vercel, environment, key, wantURL, wantToken, wantKey string
		wantError                                                                                   bool
	}{
		{name: "marketplace production", kvURL: "https://marketplace.test", kvToken: "test-secret", vercel: "1", environment: "production", wantURL: "https://marketplace.test", wantToken: "test-secret", wantKey: "wallet:production:state:v1"},
		{name: "preview isolated", kvURL: "https://marketplace.test", kvToken: "test-secret", vercel: "1", environment: "preview", wantURL: "https://marketplace.test", wantToken: "test-secret", wantKey: "wallet:preview:state:v1"},
		{name: "explicit key preserved", kvURL: "https://marketplace.test", kvToken: "test-secret", vercel: "1", key: "custom-key", wantURL: "https://marketplace.test", wantToken: "test-secret", wantKey: "custom-key"},
		{name: "upstash precedence", upURL: "https://direct.test", upToken: "direct-secret", kvURL: "https://marketplace.test", kvToken: "test-secret", wantURL: "https://direct.test", wantToken: "direct-secret", wantKey: "wallet:local:state:v1"},
		{name: "partial direct not mixed", upURL: "https://direct.test", kvURL: "https://marketplace.test", kvToken: "test-secret", wantError: true},
		{name: "partial marketplace", kvURL: "https://marketplace.test", wantError: true},
		{name: "http rejected", kvURL: "http://marketplace.test", kvToken: "test-secret", wantError: true},
		{name: "vercel missing storage", vercel: "1", environment: "production", wantError: true},
		{name: "vercel missing environment", kvURL: "https://marketplace.test", kvToken: "test-secret", vercel: "1", wantError: true},
		{name: "local memory", wantKey: "wallet:local:state:v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for k, v := range map[string]string{"UPSTASH_REDIS_REST_URL": test.upURL, "UPSTASH_REDIS_REST_TOKEN": test.upToken, "KV_REST_API_URL": test.kvURL, "KV_REST_API_TOKEN": test.kvToken, "VERCEL": test.vercel, "VERCEL_ENV": test.environment, "MOCK_STATE_KEY": test.key} {
				t.Setenv(k, v)
			}
			url, token, key, err := redisSettings("wallet")
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected configuration error: %v", err)
			}
			if !test.wantError && (url != test.wantURL || token != test.wantToken || key != test.wantKey) {
				t.Fatal("unexpected Redis configuration")
			}
		})
	}
}
func TestProfilesUseDifferentStateKeys(t *testing.T) {
	for k, v := range map[string]string{"UPSTASH_REDIS_REST_URL": "", "UPSTASH_REDIS_REST_TOKEN": "", "KV_REST_API_URL": "https://marketplace.test", "KV_REST_API_TOKEN": "test-secret", "VERCEL": "1", "VERCEL_ENV": "production", "MOCK_STATE_KEY": ""} {
		t.Setenv(k, v)
	}
	seen := map[string]bool{}
	for _, profile := range []string{"wallet", "charging-ocpi", "charging-rest"} {
		_, _, key, err := redisSettings(profile)
		if err != nil || seen[key] {
			t.Fatal("profiles must use different state keys")
		}
		seen[key] = true
	}
}
