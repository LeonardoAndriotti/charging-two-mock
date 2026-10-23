package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRESTLoadAndCAS(t *testing.T) {
	var mu sync.Mutex
	value := ""
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("authentication missing")
		}
		var args []any
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			t.Error("invalid command")
		}
		var result any
		switch args[0] {
		case "GET":
			if value != "" {
				result = value
			}
		case "EVAL":
			result = 0
			if args[4] == value {
				value = args[5].(string)
				result = 1
			}
		default:
			t.Error("unexpected command")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	})
	store := NewRedis("https://redis.test", "secret", "state")
	store.Client.Transport = localTransport{handler}
	ctx := context.Background()
	if initial, err := store.Load(ctx); initial != "" || err != nil {
		t.Fatal(initial, err)
	}
	if ok, err := store.CompareAndSwap(ctx, "", "snapshot"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := store.CompareAndSwap(ctx, "", "overwrite"); ok || err != nil {
		t.Fatal(ok, err)
	}
	if current, err := store.Load(ctx); current != "snapshot" || err != nil {
		t.Fatal(current, err)
	}
}
func TestRedisFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	store := NewRedis("https://redis.test", "secret", "state")
	store.Client.Transport = localTransport{handler}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("failure hidden")
	}
}

type localTransport struct{ handler http.Handler }

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return &http.Response{StatusCode: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String()))}, nil
}
