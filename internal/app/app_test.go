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
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestIndependentInstancesShareIdempotency(t *testing.T) {
	store := &memoryStore{conflict: true}
	one := New("wallet", "secret", store)
	two := New("wallet", "secret", store)
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
	t.Setenv("MOCK_API_TOKEN", "")
	if _, err := FromEnv("wallet"); err == nil {
		t.Fatal("missing token accepted")
	}
	t.Setenv("MOCK_API_TOKEN", "secret")
	t.Setenv("VERCEL", "1")
	t.Setenv("UPSTASH_REDIS_REST_URL", "")
	t.Setenv("UPSTASH_REDIS_REST_TOKEN", "")
	if _, err := FromEnv("wallet"); err == nil {
		t.Fatal("memory accepted on Vercel")
	}
}
