// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const maxResponseBytes int64 = 1 << 20

// NewHTTPClient returns a bounded client that refuses redirects. Refusing
// redirects prevents provider-specific authentication headers from being
// forwarded to an unexpected origin.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ReadBody reads at most one MiB and reports oversized provider responses.
func ReadBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxResponseBytes {
		return nil, errors.New("provider response exceeds 1 MiB")
	}
	return b, nil
}

// DecodeJSON decodes a size-bounded JSON response.
func DecodeJSON(r io.Reader, dst any) error {
	b, err := ReadBody(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Redact removes non-empty secrets from untrusted response bodies before they
// are included in diagnostics or logs.
func Redact(body []byte, secrets ...string) []byte {
	redacted := append([]byte(nil), body...)
	for _, secret := range secrets {
		if secret != "" {
			redacted = bytes.ReplaceAll(redacted, []byte(secret), []byte("[REDACTED]"))
		}
	}
	return redacted
}
