// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
//  Store tests
// ---------------------------------------------------------------------------

func TestNewVaultStore(t *testing.T) {
	s := NewVaultStore(60 * time.Second)
	if s == nil {
		t.Fatal("nil store")
	}
	if len(s.vaults) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestRegisterAndList(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	// Register
	body := `{"id":"v1","endpoint":"10.0.0.1:8443","mrenclave":"aabb"}`
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)

	if w.Code != 201 {
		t.Fatalf("register status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/api/vaults", nil)
	w = httptest.NewRecorder()
	s.HandleList(w, req)

	if w.Code != 200 {
		t.Fatalf("list status = %d", w.Code)
	}

	var result struct {
		Vaults []VaultRegistration `json:"vaults"`
		Count  int                 `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)

	if result.Count != 1 {
		t.Fatalf("count = %d, want 1", result.Count)
	}
	if result.Vaults[0].ID != "v1" {
		t.Fatalf("id = %q, want v1", result.Vaults[0].ID)
	}
	if result.Vaults[0].MREnclave != "aabb" {
		t.Fatalf("mrenclave = %q, want aabb", result.Vaults[0].MREnclave)
	}
}

func TestRegisterMissingFields(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	body := `{"id":"","endpoint":""}`
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHeartbeat(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	// Register first
	body := `{"id":"v1","endpoint":"10.0.0.1:8443","mrenclave":"aabb"}`
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)

	// Heartbeat
	body = `{"id":"v1"}`
	req = httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(body))
	w = httptest.NewRecorder()
	s.HandleHeartbeat(w, req)

	if w.Code != 200 {
		t.Fatalf("heartbeat status = %d", w.Code)
	}
}

func TestHeartbeatNotFound(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	body := `{"id":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/heartbeat", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleHeartbeat(w, req)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestEvictStale(t *testing.T) {
	s := NewVaultStore(1 * time.Second) // 1s TTL

	body := `{"id":"v1","endpoint":"10.0.0.1:8443","mrenclave":"aabb"}`
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleRegister(w, req)

	// Manually backdate the heartbeat
	s.mu.Lock()
	s.vaults["v1"].LastHeartbeat = time.Now().Add(-5 * time.Second)
	s.mu.Unlock()

	s.evictStale()

	s.mu.RLock()
	_, ok := s.vaults["v1"]
	s.mu.RUnlock()

	if ok {
		t.Fatal("expected vault to be evicted")
	}
}

func TestRegisterMultipleVaults(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	for i := 0; i < 10; i++ {
		body := `{"id":"v` + string(rune('0'+i)) + `","endpoint":"10.0.0.1:` + string(rune('0'+i)) + `443","mrenclave":"aabb"}`
		req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.HandleRegister(w, req)
		if w.Code != 201 {
			t.Fatalf("register vault %d: status = %d", i, w.Code)
		}
	}

	req := httptest.NewRequest("GET", "/api/vaults", nil)
	w := httptest.NewRecorder()
	s.HandleList(w, req)

	var result struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Count != 10 {
		t.Fatalf("count = %d, want 10", result.Count)
	}
}

func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != 200 {
		t.Fatalf("health status = %d", w.Code)
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["status"] != "ok" {
		t.Fatal("expected status ok")
	}
}

func TestWrongMethods(t *testing.T) {
	s := NewVaultStore(60 * time.Second)

	tests := []struct {
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{"GET", "/api/register", s.HandleRegister},
		{"GET", "/api/heartbeat", s.HandleHeartbeat},
		{"POST", "/api/vaults", s.HandleList},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		tt.handler(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tt.method, tt.path, w.Code)
		}
	}
}
