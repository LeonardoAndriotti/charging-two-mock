// Package storage supplies optimistic, atomic snapshot persistence for small mocks.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Store interface {
	Load(context.Context) (string, error)
	CompareAndSwap(context.Context, string, string) (bool, error)
}
type Redis struct {
	URL, Token, Key string
	Client          *http.Client
}

func NewRedis(url, token, key string) *Redis {
	return &Redis{url, token, key, &http.Client{Timeout: 5 * time.Second}}
}
func (s *Redis) command(ctx context.Context, args ...any) (json.RawMessage, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.Client.Do(req)
	if err != nil {
		return nil, errors.New("redis request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("redis HTTP error")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Error != "" {
		return nil, errors.New("redis command failed")
	}
	return envelope.Result, nil
}
func (s *Redis) Load(ctx context.Context) (string, error) {
	raw, err := s.command(ctx, "GET", s.Key)
	if err != nil {
		return "", err
	}
	if string(raw) == "null" {
		return "", nil
	}
	var value string
	err = json.Unmarshal(raw, &value)
	return value, err
}

const casScript = `local current = redis.call('GET', KEYS[1]) or ''; if current ~= ARGV[1] then return 0 end; redis.call('SET', KEYS[1], ARGV[2]); return 1`

func (s *Redis) CompareAndSwap(ctx context.Context, old, next string) (bool, error) {
	raw, err := s.command(ctx, "EVAL", casScript, 1, s.Key, old, next)
	if err != nil {
		return false, err
	}
	var result int
	err = json.Unmarshal(raw, &result)
	return result == 1, err
}
