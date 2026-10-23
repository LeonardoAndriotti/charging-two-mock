package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
)

func call(s *Server, method, path, key, body string) (int, map[string]any) {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer local-test-token")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func must(t *testing.T, s *Server, path, key, body string, want int) map[string]any {
	t.Helper()
	status, out := call(s, "POST", path, key, body)
	if status != want {
		t.Fatalf("%s: %d %v", path, status, out)
	}
	return out
}
func funded(t *testing.T, s *Server) string {
	t.Helper()
	v := must(t, s, "/v1/wallets", "wallet", "{}", 201)
	id := v["id"].(string)
	op := must(t, s, "/v1/topups", "topup", fmt.Sprintf(`{"wallet_id":%q,"amount_cents":10000}`, id), 201)
	must(t, s, "/_mock/operations/"+op["id"].(string)+"/confirm", "confirm", "{}", 200)
	return id
}
func TestWalletLifecycle(t *testing.T) {
	s := New("wallet")
	id := funded(t, s)
	body := fmt.Sprintf(`{"wallet_id":%q,"amount_cents":6000}`, id)
	op := must(t, s, "/v1/holds", "hold", body, 201)
	repeat := must(t, s, "/v1/holds", "hold", body, 201)
	if repeat["id"] != op["id"] {
		t.Fatal("duplicate hold")
	}
	must(t, s, "/v1/holds", "hold", fmt.Sprintf(`{"wallet_id":%q,"amount_cents":1}`, id), 409)
	hold := op["id"].(string)
	must(t, s, "/v1/holds/"+hold+"/capture", "capture", `{"amount_cents":2500}`, 200)
	must(t, s, "/v1/operations/"+hold+"/refund", "refund", "{}", 200)
	must(t, s, "/v1/operations/"+hold+"/refund", "refund-again", "{}", 409)
	_, balance := call(s, "GET", "/v1/wallets/"+id, "", "")
	if balance["available_cents"] != float64(10000) || balance["reserved_cents"] != float64(0) {
		t.Fatal(balance)
	}
}
func TestConcurrentReservations(t *testing.T) {
	s := New("wallet")
	id := funded(t, s)
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _ := call(s, "POST", "/v1/holds", fmt.Sprint(i), fmt.Sprintf(`{"wallet_id":%q,"amount_cents":1000}`, id))
			if status == 201 {
				mu.Lock()
				success++
				mu.Unlock()
			} else if status != 409 {
				t.Errorf("status %d", status)
			}
		}(i)
	}
	wg.Wait()
	if success != 10 {
		t.Fatal(success)
	}
}
func TestChargingProfiles(t *testing.T) {
	for _, profile := range []string{"charging-ocpi", "charging-rest"} {
		t.Run(profile, func(t *testing.T) {
			s := New(profile)
			v := must(t, s, "/v1/sessions", "start", `{"connector_id":"connector-1"}`, 202)
			id := v["id"].(string)
			must(t, s, "/v1/sessions", "busy", `{"connector_id":"connector-1"}`, 409)
			must(t, s, "/v1/sessions/"+id+"/stop", "early", "{}", 409)
			must(t, s, "/_mock/sessions/"+id+"/activate", "activate", "{}", 200)
			must(t, s, "/_mock/sessions/"+id+"/meter", "meter", `{"energy_wh":12000}`, 200)
			must(t, s, "/_mock/sessions/"+id+"/meter", "backwards", `{"energy_wh":1}`, 409)
			must(t, s, "/v1/sessions/"+id+"/stop", "stop", "{}", 200)
			status, _ := call(s, "GET", "/v1/sessions/"+id+"/"+s.documentName(), "", "")
			if status != 404 {
				t.Fatal(status)
			}
			must(t, s, "/_mock/sessions/"+id+"/complete", "complete", "{}", 200)
			status, cdr := call(s, "GET", "/v1/sessions/"+id+"/"+s.documentName(), "", "")
			if status != 200 {
				t.Fatal(status)
			}
			session := cdr["session"].(map[string]any)
			if session["cost_cents"] != float64(12*s.tariff()) {
				t.Fatal(cdr)
			}
		})
	}
}
func TestTransportValidation(t *testing.T) {
	s := New("wallet")
	r := httptest.NewRequest("POST", "/v1/wallets", bytes.NewBufferString("{}"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	must(t, s, "/v1/wallets", "", "{}", 400)
	must(t, s, "/v1/wallets", "bad", "{}{}", 400)
	must(t, s, "/v1/wallets", "unknown", `{"typo":1}`, 400)
	for _, scenario := range []string{"reject", "unavailable", "unknown"} {
		r := httptest.NewRequest("POST", "/v1/wallets", bytes.NewBufferString("{}"))
		r.Header.Set("Authorization", "Bearer local-test-token")
		r.Header.Set("X-Mock-Scenario", scenario)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestTimeoutAfterCommitRecovery(t *testing.T) {
	s := New("wallet")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/v1/wallets", bytes.NewBufferString("{}")).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer local-test-token")
	r.Header.Set("Idempotency-Key", "uncertain")
	r.Header.Set("X-Mock-Scenario", "timeout-after-commit")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	recovered := must(t, s, "/v1/wallets", "uncertain", "{}", 201)
	if len(s.wallets) != 1 || recovered["id"] == nil {
		t.Fatal("uncertain result duplicated")
	}
}

func TestTimeoutBeforeCommitCancellation(t *testing.T) {
	s := New("wallet")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/v1/wallets", bytes.NewBufferString("{}")).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer local-test-token")
	r.Header.Set("Idempotency-Key", "cancelled")
	r.Header.Set("X-Mock-Scenario", "timeout")
	s.ServeHTTP(httptest.NewRecorder(), r)
	if len(s.wallets) != 0 {
		t.Fatal("cancelled request committed")
	}
}
