// Copyright (c) Privasys. All rights reserved.
// Licensed under the GNU Affero General Public License v3.0. See LICENSE file for details.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
//  Attestation server verification — verifies a vault's attestation quote
//  via the Privasys attestation server (TEE-agnostic: SGX, TDX, SEV-SNP,
//  NVIDIA, ARM CCA).
// ---------------------------------------------------------------------------

// verifyResponse mirrors VerifyResponse from the attestation server.
type verifyResponse struct {
	Success   bool   `json:"success"`
	Status    string `json:"status,omitempty"`
	TeeType   string `json:"teeType,omitempty"`
	MREnclave string `json:"mrenclave,omitempty"`
	MRSigner  string `json:"mrsigner,omitempty"`
	Error     string `json:"error,omitempty"`
}

// verifyVaultQuote sends a vault's attestation quote to the attestation
// server for verification.  Returns nil if verification passed.
func verifyVaultQuote(quoteB64 string) error {
	body, _ := json.Marshal(map[string]string{"quote": quoteB64})

	req, err := http.NewRequest("POST", attestationVerifyURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if attestationAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+attestationAPIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("attestation server request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result verifyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("invalid attestation server response: %s", string(respBody))
	}

	if !result.Success {
		return fmt.Errorf("%s: %s", result.Status, result.Error)
	}

	return nil
}
