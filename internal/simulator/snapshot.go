package simulator

import "encoding/json"

type snapshot struct {
	Seq        int
	Wallets    map[string]Wallet
	Operations map[string]Operation
	Sessions   map[string]Session
	Events     []Event
	Keys       map[string]cached
}

func (s *Server) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(snapshot{s.seq, s.wallets, s.operations, s.sessions, s.events, s.keys})
}
func (s *Server) Restore(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var v snapshot
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.seq = v.Seq
	s.wallets = v.Wallets
	s.operations = v.Operations
	s.sessions = v.Sessions
	s.events = v.Events
	s.keys = v.Keys
	if s.wallets == nil {
		s.wallets = map[string]Wallet{}
	}
	if s.operations == nil {
		s.operations = map[string]Operation{}
	}
	if s.sessions == nil {
		s.sessions = map[string]Session{}
	}
	if s.keys == nil {
		s.keys = map[string]cached{}
	}
	return nil
}
