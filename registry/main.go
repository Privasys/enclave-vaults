// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

// Attested Registry — a service-discovery endpoint for Enclave Vault
// constellations.
//
// Each Enclave Vault self-registers on startup by POSTing its endpoint
// and RA-TLS certificate (containing an SGX DCAP quote) to the registry.
// The registry verifies the quote via the attestation server before
// adding the vault to the pool. Clients query the registry to discover
// the set of live, attested vault instances.
//
// The registry runs inside an enclave-os-virtual Confidential VM (TDX),
// providing end-to-end attestation of the registry itself.

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
//  Configuration (via environment variables)
// ---------------------------------------------------------------------------

var (
	// LISTEN_ADDR is the address to bind to (default ":8080").
	listenAddr = envOrDefault("LISTEN_ADDR", ":8080")

	// ATTESTATION_VERIFY_URL is the attestation server's verify endpoint.
	// Each vault's RA-TLS certificate must pass attestation verification.
	// The attestation server is TEE-agnostic (SGX, TDX, SEV-SNP, etc.).
	attestationVerifyURL = envOrDefault("ATTESTATION_VERIFY_URL", "")

	// ATTESTATION_API_KEY is the bearer token for the attestation server.
	attestationAPIKey = envOrDefault("ATTESTATION_API_KEY", "")

	// VAULT_TTL_SECONDS is the time (in seconds) a vault registration is
	// valid without heartbeat before being evicted (default: 60).
	vaultTTLSeconds = envOrDefaultInt("VAULT_TTL_SECONDS", 60)
)

// ---------------------------------------------------------------------------
//  Entry point
// ---------------------------------------------------------------------------

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	store := NewVaultStore(time.Duration(vaultTTLSeconds) * time.Second)

	// Start eviction loop
	go store.EvictionLoop(30 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/register", store.HandleRegister)
	mux.HandleFunc("/api/heartbeat", store.HandleHeartbeat)
	mux.HandleFunc("/api/vaults", store.HandleList)
	mux.HandleFunc("/api/health", handleHealth)

	log.Println("--- Enclave Vaults — Attested Registry ---")
	log.Printf("Listening on %s", listenAddr)
	log.Println("Endpoints:")
	log.Println("  POST /api/register   Register a vault")
	log.Println("  POST /api/heartbeat  Vault heartbeat")
	log.Println("  GET  /api/vaults     List live vaults")
	log.Println("  GET  /api/health     Health check")
	if attestationVerifyURL != "" {
		log.Printf("Attestation verification: %s", attestationVerifyURL)
	} else {
		log.Println("WARNING: ATTESTATION_VERIFY_URL not set \u2014 vault quotes will NOT be verified")
	}

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
//  VaultStore — in-memory registry of live vault instances
// ---------------------------------------------------------------------------

// VaultRegistration represents a single registered vault instance.
type VaultRegistration struct {
	// ID is a unique identifier for this vault instance (self-generated UUID).
	ID string `json:"id"`

	// Endpoint is the RA-TLS endpoint (e.g. "10.0.0.5:8443").
	Endpoint string `json:"endpoint"`

	// MREnclave is the hex-encoded MRENCLAVE measurement of the vault enclave.
	MREnclave string `json:"mrenclave"`

	// MRSigner is the hex-encoded MRSIGNER measurement.
	MRSigner string `json:"mrsigner,omitempty"`

	// RegisteredAt is the UTC timestamp when this vault registered.
	RegisteredAt time.Time `json:"registeredAt"`

	// LastHeartbeat is the UTC timestamp of the last heartbeat.
	LastHeartbeat time.Time `json:"lastHeartbeat"`

	// Status is the current vault status ("active", "stale").
	Status string `json:"status"`
}

// VaultStore is a thread-safe in-memory store of vault registrations.
type VaultStore struct {
	mu     sync.RWMutex
	vaults map[string]*VaultRegistration // keyed by ID
	ttl    time.Duration
}

// NewVaultStore creates a new store with the given TTL.
func NewVaultStore(ttl time.Duration) *VaultStore {
	return &VaultStore{
		vaults: make(map[string]*VaultRegistration),
		ttl:    ttl,
	}
}

// EvictionLoop periodically removes stale vault registrations.
func (s *VaultStore) EvictionLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.evictStale()
	}
}

func (s *VaultStore) evictStale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-s.ttl)
	for id, v := range s.vaults {
		if v.LastHeartbeat.Before(cutoff) {
			log.Printf("Evicting stale vault %s (%s) — last heartbeat %s",
				id, v.Endpoint, v.LastHeartbeat.Format(time.RFC3339))
			delete(s.vaults, id)
		}
	}
}

// ---------------------------------------------------------------------------
//  Handlers
// ---------------------------------------------------------------------------

// RegisterRequest is the JSON body for POST /api/register.
type RegisterRequest struct {
	ID        string `json:"id"`
	Endpoint  string `json:"endpoint"`
	QuoteB64  string `json:"quote,omitempty"` // base64-encoded raw attestation quote
	MREnclave string `json:"mrenclave"`
	MRSigner  string `json:"mrsigner,omitempty"`
}

// HandleRegister processes a vault self-registration.
func (s *VaultStore) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "invalid JSON body")
		return
	}
	if req.ID == "" || req.Endpoint == "" {
		jsonError(w, 400, "id and endpoint are required")
		return
	}

	// Optionally verify the vault's attestation quote
	if attestationVerifyURL != "" && req.QuoteB64 != "" {
		if err := verifyVaultQuote(req.QuoteB64); err != nil {
			log.Printf("Attestation verification failed for vault %s: %v", req.ID, err)
			jsonError(w, 403, "attestation verification failed: "+err.Error())
			return
		}
		log.Printf("Attestation verification PASSED for vault %s (%s)", req.ID, req.Endpoint)
	}

	now := time.Now().UTC()
	reg := &VaultRegistration{
		ID:            req.ID,
		Endpoint:      req.Endpoint,
		MREnclave:     req.MREnclave,
		MRSigner:      req.MRSigner,
		RegisteredAt:  now,
		LastHeartbeat: now,
		Status:        "active",
	}

	s.mu.Lock()
	s.vaults[req.ID] = reg
	s.mu.Unlock()

	log.Printf("Registered vault %s at %s (MRENCLAVE=%s)", req.ID, req.Endpoint, req.MREnclave)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]string{"status": "registered", "id": req.ID})
}

// HeartbeatRequest is the JSON body for POST /api/heartbeat.
type HeartbeatRequest struct {
	ID string `json:"id"`
}

// HandleHeartbeat updates the last_heartbeat timestamp for a registered vault.
func (s *VaultStore) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "invalid JSON body")
		return
	}

	s.mu.Lock()
	v, ok := s.vaults[req.ID]
	if ok {
		v.LastHeartbeat = time.Now().UTC()
		v.Status = "active"
	}
	s.mu.Unlock()

	if !ok {
		jsonError(w, 404, "vault not found — re-register")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleList returns the list of active vault registrations.
func (s *VaultStore) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	result := make([]*VaultRegistration, 0, len(s.vaults))
	for _, v := range s.vaults {
		result = append(result, v)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"vaults": result,
		"count":  len(result),
	})
}

// ---------------------------------------------------------------------------
//  Helpers
// ---------------------------------------------------------------------------

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := json.Number(v).Int64(); err == nil {
		// Parse properly
		var i int64
		json.Unmarshal([]byte(v), &i)
		n = int(i)
	}
	if n <= 0 {
		return fallback
	}
	return n
}
