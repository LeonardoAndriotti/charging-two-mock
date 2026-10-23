// Package simulator implements experimental ElecPay contracts, not vendor replicas.
package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Wallet struct {
	ID        string `json:"id"`
	Available int64  `json:"available_cents"`
	Reserved  int64  `json:"reserved_cents"`
}
type Operation struct {
	ID       string `json:"id"`
	WalletID string `json:"wallet_id"`
	Amount   int64  `json:"amount_cents"`
	Status   string `json:"status"`
}
type Session struct {
	ID        string `json:"id"`
	Connector string `json:"connector_id"`
	Status    string `json:"status"`
	Wh        int64  `json:"energy_wh"`
	Cost      int64  `json:"cost_cents"`
}
type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data any    `json:"data"`
}
type cached struct {
	Fingerprint string
	Status      int
	Body        json.RawMessage
}
type Server struct {
	mu         sync.Mutex
	profile    string
	seq        int
	wallets    map[string]Wallet
	operations map[string]Operation
	sessions   map[string]Session
	events     []Event
	keys       map[string]cached
}

func New(profile string) *Server {
	return &Server{profile: profile, wallets: map[string]Wallet{}, operations: map[string]Operation{}, sessions: map[string]Session{}, keys: map[string]cached{}, events: []Event{}}
}
func (s *Server) id(prefix string) string { s.seq++; return fmt.Sprintf("%s_%06d", prefix, s.seq) }
func (s *Server) emit(kind string, data any) {
	s.events = append(s.events, Event{s.id("evt"), kind, data})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == "GET" {
		write(w, 200, map[string]string{"status": "ok", "profile": s.profile})
		return
	}
	// Delay runs before state mutation, allowing deterministic timeout tests.
	switch r.Header.Get("X-Mock-Scenario") {
	case "unavailable":
		fail(w, 503, "provider_unavailable")
		return
	case "timeout":
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
		}
	case "reject":
		fail(w, 422, "provider_rejected")
		return
	case "", "timeout-after-commit":
	default:
		fail(w, 400, "unknown_scenario")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "invalid_body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == "GET" {
		s.get(w, r)
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		fail(w, 400, "idempotency_key_required")
		return
	}
	fingerprint := r.URL.Path + "\n" + string(body)
	if c, ok := s.keys[key]; ok {
		if c.Fingerprint != fingerprint {
			fail(w, 409, "idempotency_conflict")
			return
		}
		write(w, c.Status, c.Body)
		return
	}
	var in struct {
		WalletID  string `json:"wallet_id"`
		Amount    int64  `json:"amount_cents"`
		Connector string `json:"connector_id"`
		Wh        int64  `json:"energy_wh"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, 400, "invalid_json")
		return
	}
	status, out := s.post(r, &in.WalletID, in.Amount, in.Connector, in.Wh)
	if status < 300 {
		encoded, _ := json.Marshal(out)
		s.keys[key] = cached{fingerprint, status, encoded}
	}
	if r.Header.Get("X-Mock-Scenario") == "timeout-after-commit" && status < 300 {
		// State and idempotency record already committed. Release mutex while waiting.
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		s.mu.Lock()
	}
	write(w, status, out)
}
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch path {
	case "/v1/events":
		if s.profile == "charging-rest" {
			fail(w, 404, "poll_sessions_instead")
			return
		}
		write(w, 200, s.events)
	case "/v1/capabilities":
		write(w, 200, map[string]any{"profile": s.profile, "contract": "elecpay-experimental-v1", "vendor_compatible": false, "webhook_delivery": false, "event_polling": s.profile != "charging-rest", "session_polling": s.profile != "wallet", "final_document": s.documentName()})
	case "/v1/connectors":
		if s.profile == "wallet" {
			fail(w, 404, "not_found")
			return
		}
		state := "AVAILABLE"
		for _, v := range s.sessions {
			if v.Status != "COMPLETED" {
				state = "OCCUPIED"
			}
		}
		write(w, 200, []any{map[string]any{"id": "connector-1", "status": state, "currency": "BRL", "price_cents_per_kwh": s.tariff()}})
	default:

		// Exact resource paths avoid accepting arbitrary trailing segments.
		for id, v := range s.wallets {
			if path == "/v1/wallets/"+id {
				write(w, 200, v)
				return
			}
		}
		for id, v := range s.operations {
			if path == "/v1/operations/"+id {
				write(w, 200, v)
				return
			}
		}
		for id, v := range s.sessions {
			if path == "/v1/sessions/"+id {
				write(w, 200, v)
				return
			}
			if path == "/v1/sessions/"+id+"/"+s.documentName() && v.Status == "COMPLETED" {
				write(w, 200, map[string]any{"id": "cdr-" + id, "session": v, "currency": "BRL"})
				return
			}
		}
		fail(w, 404, "not_found")
	}
}
func (s *Server) tariff() int64 {
	if s.profile == "charging-rest" {
		return 250
	}
	return 200
}
func (s *Server) documentName() string {
	if s.profile == "charging-rest" {
		return "receipt"
	}
	return "cdr"
}
func resultError(status int, msg string) (int, any) { return status, map[string]string{"error": msg} }
func (s *Server) post(r *http.Request, walletID *string, amount int64, connector string, wh int64) (int, any) {
	path := r.URL.Path
	if s.profile == "wallet" {
		switch path {
		case "/v1/wallets":
			v := Wallet{ID: s.id("wal")}
			s.wallets[v.ID] = v
			return 201, v
		case "/v1/topups", "/v1/holds":
			v, ok := s.wallets[*walletID]
			if !ok {
				return resultError(404, "wallet_not_found")
			}
			if amount <= 0 || amount > 100000000 {
				return resultError(400, "invalid_amount")
			}
			op := Operation{s.id("op"), v.ID, amount, "PENDING"}
			if path == "/v1/holds" {
				if v.Available < amount {
					return resultError(409, "insufficient_funds")
				}
				v.Available -= amount
				v.Reserved += amount
				op.Status = "HELD"
				s.wallets[v.ID] = v
			}
			s.operations[op.ID] = op
			s.emit("operation.created", op)
			return 201, op
		}
		for id, op := range s.operations {
			v := s.wallets[op.WalletID]
			switch path {
			case "/_mock/operations/" + id + "/confirm":
				if op.Status != "PENDING" {
					return resultError(409, "invalid_transition")
				}
				op.Status = "CONFIRMED"
				v.Available += op.Amount
			case "/v1/holds/" + id + "/capture":
				if op.Status != "HELD" {
					return resultError(409, "invalid_transition")
				}
				if amount <= 0 || amount > op.Amount {
					return resultError(400, "invalid_capture")
				}
				v.Reserved -= op.Amount
				v.Available += op.Amount - amount
				op.Amount = amount
				op.Status = "CAPTURED"
			case "/v1/holds/" + id + "/release":
				if op.Status != "HELD" {
					return resultError(409, "invalid_transition")
				}
				v.Reserved -= op.Amount
				v.Available += op.Amount
				op.Status = "RELEASED"
			case "/v1/operations/" + id + "/refund":
				if op.Status != "CAPTURED" {
					return resultError(409, "invalid_transition")
				}
				v.Available += op.Amount
				op.Status = "REFUNDED"
			default:
				continue
			}
			s.wallets[v.ID] = v
			s.operations[id] = op
			s.emit("operation."+op.Status, op)
			return 200, op
		}
	} else {
		if path == "/v1/sessions" {
			if connector != "connector-1" {
				return resultError(404, "connector_not_found")
			}
			for _, v := range s.sessions {
				if v.Status != "COMPLETED" {
					return resultError(409, "connector_busy")
				}
			}
			v := Session{ID: s.id("session"), Connector: connector, Status: "PENDING"}
			s.sessions[v.ID] = v
			s.emit("session.requested", v)
			return 202, v
		}
		for id, v := range s.sessions {
			switch path {
			case "/_mock/sessions/" + id + "/activate":
				if v.Status != "PENDING" {
					return resultError(409, "invalid_transition")
				}
				v.Status = "ACTIVE"
			case "/_mock/sessions/" + id + "/meter":
				if v.Status != "ACTIVE" || wh < v.Wh || wh > 1000000 {
					return resultError(409, "invalid_meter")
				}
				v.Wh = wh
			case "/v1/sessions/" + id + "/stop":
				if v.Status != "ACTIVE" {
					return resultError(409, "invalid_transition")
				}
				v.Status = "STOP_REQUESTED"
			case "/_mock/sessions/" + id + "/complete":
				if v.Status != "STOP_REQUESTED" {
					return resultError(409, "invalid_transition")
				}
				v.Status = "COMPLETED"
				v.Cost = (v.Wh*s.tariff() + 999) / 1000
			default:
				continue
			}
			s.sessions[id] = v
			s.emit("session."+v.Status, v)
			return 200, v
		}
	}
	return resultError(404, "not_found")
}
