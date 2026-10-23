package app

import (
	"bytes"
	"crypto/subtle"
	"elecpay.local/charging-two-mock/internal/simulator"
	"elecpay.local/charging-two-mock/internal/storage"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

func reply(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func FromEnv(profile string) (http.Handler, error) {
	token := os.Getenv("MOCK_API_TOKEN")
	if token == "" {
		return nil, errors.New("MOCK_API_TOKEN is required")
	}
	url, secret := os.Getenv("UPSTASH_REDIS_REST_URL"), os.Getenv("UPSTASH_REDIS_REST_TOKEN")
	var store storage.Store
	if url != "" || secret != "" {
		if !strings.HasPrefix(url, "https://") || secret == "" {
			return nil, errors.New("Redis HTTPS URL and token are required together")
		}
		key := os.Getenv("MOCK_STATE_KEY")
		if key == "" {
			key = profile + ":local:state:v1"
		}
		if os.Getenv("VERCEL") != "" && os.Getenv("MOCK_STATE_KEY") == "" {
			return nil, errors.New("MOCK_STATE_KEY must isolate the deployment environment")
		}
		store = storage.NewRedis(url, secret, key)
	}
	if os.Getenv("VERCEL") != "" && store == nil {
		return nil, errors.New("persistent Redis storage is required on Vercel")
	}
	return New(profile, token, store), nil
}
func New(profile, token string, store storage.Store) http.Handler {
	local := simulator.New(profile)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			local.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			reply(w, 401, "invalid_token")
			return
		}
		request := r.Clone(r.Context())
		request.Header = request.Header.Clone()
		request.Header.Set("Authorization", "Bearer local-test-token")
		if store == nil {
			local.ServeHTTP(w, request)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			reply(w, 400, "invalid_body")
			return
		}
		scenario := request.Header.Get("X-Mock-Scenario")
		if scenario == "timeout-after-commit" {
			request.Header.Del("X-Mock-Scenario")
		}
		for attempt := 0; attempt < 8; attempt++ {
			previous, err := store.Load(r.Context())
			if err != nil {
				reply(w, 503, "storage_unavailable")
				return
			}
			engine := simulator.New(profile)
			if engine.Restore([]byte(previous)) != nil {
				reply(w, 503, "invalid_stored_state")
				return
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if r.Context().Err() != nil {
				return
			}
			if request.Method == "POST" && recorder.Code < 300 {
				next, err := engine.Snapshot()
				if err != nil {
					reply(w, 500, "serialization_failed")
					return
				}
				if len(next) > 2<<20 {
					reply(w, 503, "mock_state_limit_reached")
					return
				}
				saved, err := store.CompareAndSwap(r.Context(), previous, string(next))
				if err != nil {
					reply(w, 503, "commit_uncertain_retry_same_key")
					return
				}
				if !saved {
					continue
				}
				if scenario == "timeout-after-commit" {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(3 * time.Second):
					}
				}
			}
			for k, values := range recorder.Header() {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		reply(w, 503, "concurrent_update_retry_same_key")
	})
}
