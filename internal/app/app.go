package app

import (
	"bytes"
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
	url, secret, key, err := redisSettings(profile)
	if err != nil {
		return nil, err
	}
	var store storage.Store
	if url != "" {
		store = storage.NewRedis(url, secret, key)
	}
	return New(profile, store), nil
}

// redisSettings accepts both direct Upstash credentials and Marketplace aliases.
// A partially configured pair is an error; credentials from different pairs are never mixed.
func redisSettings(profile string) (string, string, string, error) {
	url, secret := os.Getenv("UPSTASH_REDIS_REST_URL"), os.Getenv("UPSTASH_REDIS_REST_TOKEN")
	if url == "" && secret == "" {
		url, secret = os.Getenv("KV_REST_API_URL"), os.Getenv("KV_REST_API_TOKEN")
	}
	if url != "" || secret != "" {
		if !strings.HasPrefix(url, "https://") || secret == "" {
			return "", "", "", errors.New("Redis HTTPS URL and token are required together")
		}
	} else if os.Getenv("VERCEL") != "" {
		return "", "", "", errors.New("persistent Redis storage is required on Vercel: configure KV_REST_API_URL and KV_REST_API_TOKEN or the UPSTASH_REDIS_REST pair")
	}
	key := os.Getenv("MOCK_STATE_KEY")
	if key == "" {
		environment := "local"
		if os.Getenv("VERCEL") != "" {
			environment = os.Getenv("VERCEL_ENV")
			if environment == "" {
				return "", "", "", errors.New("VERCEL_ENV or explicit MOCK_STATE_KEY is required on Vercel")
			}
		}
		key = profile + ":" + environment + ":state:v1"
	}
	return url, secret, key, nil
}
func New(profile string, store storage.Store) http.Handler {
	local := simulator.New(profile)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			local.ServeHTTP(w, r)
			return
		}
		request := r.Clone(r.Context())
		request.Header = request.Header.Clone()
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
