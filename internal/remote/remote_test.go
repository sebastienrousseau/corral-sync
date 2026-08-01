// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: GPL-3.0-only

package remote

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestFatalErrors(t *testing.T) {
	if Fatal(nil) != nil {
		t.Fatal("Fatal(nil) must be nil")
	}
	base := errors.New("failed")
	err := Fatal(base)
	if !IsFatal(err) || !errors.Is(err, base) || err.Error() != "failed" || IsFatal(base) {
		t.Fatalf("unexpected fatal behavior: %v", err)
	}
}

func TestValidateRepo(t *testing.T) {
	if err := ValidateRepo(Repo{Name: "valid-repo", Visibility: Private}); err != nil {
		t.Fatal(err)
	}
	bad := []Repo{
		{Name: "", Visibility: Private}, {Name: ".", Visibility: Private},
		{Name: "..", Visibility: Private}, {Name: "a/b", Visibility: Private},
		{Name: strings.Repeat("a", 101), Visibility: Private},
		{Name: "repo", Visibility: "internal"},
	}
	for _, r := range bad {
		if err := ValidateRepo(r); err == nil {
			t.Fatalf("expected invalid repo: %+v", r)
		}
	}
}

func TestValidateCloneURL(t *testing.T) {
	valid := []string{
		"git@example.com:owner/repo.git",
		"ssh://git@example.com/owner/repo.git",
		"https://example.com/owner/repo.git",
	}
	for _, raw := range valid {
		if err := ValidateCloneURL(raw); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	invalid := []string{
		"", "-bad", "git@example.com", "git@:repo", "git@example.com:/repo",
		"http://example.com/repo", "file:///tmp/repo", "/tmp/repo",
		"https://user:pass@example.com/repo", "ssh://example.com/repo", "https://example.com/repo\nnext",
	}
	for _, raw := range invalid {
		if err := ValidateCloneURL(raw); err == nil {
			t.Fatalf("expected %q to fail", raw)
		}
	}
}

func TestHTTPHelpers(t *testing.T) {
	c := NewHTTPClient()
	if c.Timeout == 0 {
		t.Fatal("client timeout is zero")
	}
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v", err)
	}
	b, err := ReadBody(strings.NewReader("ok"))
	if err != nil || string(b) != "ok" {
		t.Fatalf("ReadBody = %q, %v", b, err)
	}
	if _, err := ReadBody(strings.NewReader(strings.Repeat("x", int(maxResponseBytes)+1))); err == nil {
		t.Fatal("expected oversized body error")
	}
	if _, err := ReadBody(failingReader{}); err == nil {
		t.Fatal("expected read error")
	}
	var decoded map[string]int
	if err := DecodeJSON(strings.NewReader(`{"value":1}`), &decoded); err != nil || decoded["value"] != 1 {
		t.Fatalf("DecodeJSON = %v, %v", decoded, err)
	}
	if err := DecodeJSON(strings.NewReader("{"), &decoded); err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if err := DecodeJSON(failingReader{}, &decoded); err == nil {
		t.Fatal("expected decode read error")
	}
}

func TestRedact(t *testing.T) {
	input := []byte("token=secret and secret again")
	got := Redact(input, "", "secret")
	if string(got) != "token=[REDACTED] and [REDACTED] again" || string(input) != "token=secret and secret again" {
		t.Fatalf("redaction = %q, input = %q", got, input)
	}
}

func FuzzValidateCloneURL(f *testing.F) {
	for _, seed := range []string{"git@example.com:a/b.git", "https://example.com/a.git", "", "-x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_ = ValidateCloneURL(raw)
	})
}
