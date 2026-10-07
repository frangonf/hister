// SPDX-License-Identifier: AGPL-3.0-or-later
package defuddle

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/asciimoo/hister/server/document"
	"github.com/asciimoo/hister/server/extractor/sdk"
)

// buildStub compiles the protocol test double once per test and returns its
// path. The stub selects its behavior from markers in the input HTML.
func buildStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/stub-bridge"
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/stub").CombinedOutput()
	if err != nil {
		t.Fatalf("building stub bridge: %v: %s", err, out)
	}
	return bin
}

func newExtractor(t *testing.T, options map[string]any) *DefuddleExtractor {
	t.Helper()
	e := &DefuddleExtractor{}
	cfg := &sdk.Config{Enable: true, Options: options}
	if err := e.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	return e
}

func TestDefaultConfig(t *testing.T) {
	e := &DefuddleExtractor{}
	cfg := e.GetConfig()
	if cfg.Enable {
		t.Error("defuddle must be disabled by default: it requires an external binary")
	}
	if got := cfg.Options["binary"]; got != "defuddle-bridge" {
		t.Errorf("default binary = %v, want defuddle-bridge", got)
	}
	if got := cfg.Options["timeout"]; got != 10 {
		t.Errorf("default timeout = %v, want 10", got)
	}
	if got := cfg.Options["max_concurrent_jobs"]; got != 2 {
		t.Errorf("default max_concurrent_jobs = %v, want 2", got)
	}
}

func TestSetConfigRejectsUnknownOption(t *testing.T) {
	e := &DefuddleExtractor{}
	err := e.SetConfig(&sdk.Config{Options: map[string]any{"nonsense": true}})
	if err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("unknown option error = %v, want unknown option rejection", err)
	}
}

func TestSetConfigRejectsInvalidMaxConcurrentJobs(t *testing.T) {
	for name, value := range map[string]any{
		"negative":   -1,
		"non-number": "many",
		"fractional": 1.5,
	} {
		t.Run(name, func(t *testing.T) {
			e := &DefuddleExtractor{}
			err := e.SetConfig(&sdk.Config{Options: map[string]any{"max_concurrent_jobs": value}})
			if err == nil {
				t.Errorf("max_concurrent_jobs = %v: want error", value)
			}
		})
	}
}

func TestMatchAlwaysMatches(t *testing.T) {
	e := &DefuddleExtractor{}
	for _, url := range []string{"https://example.com/a", "http://localhost/x", ""} {
		if !e.Match(&document.Document{URL: url}) {
			t.Errorf("Match(%q) = false, want true (generic extractor)", url)
		}
	}
}

func TestExtractSuccess(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": buildStub(t)})
	d := &document.Document{URL: "https://example.com/article", HTML: "<html><body>article</body></html>"}

	result := e.Extract(d)
	if result.Decision() != sdk.ExtractorSuccess {
		t.Fatalf("decision = %v, want success (err: %v)", result.Decision(), result.Err())
	}
	if d.Title != "Stub Title" {
		t.Errorf("title = %q, want Stub Title", d.Title)
	}
	if d.Text != "stub article text" {
		t.Errorf("text = %q, want stub article text", d.Text)
	}
	for key, want := range map[string]string{
		"author":      "Stub Author",
		"published":   "2026-01-15T10:00:00Z",
		"site_name":   "Example",
		"description": "Stub description",
		"image":       "https://example.com/img.png",
		"language":    "en",
	} {
		if got := d.Metadata[key]; got != want {
			t.Errorf("metadata[%s] = %v, want %v", key, got, want)
		}
	}
}

func TestExtractFallsBackBelowWordThreshold(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": buildStub(t)})
	d := &document.Document{URL: "https://example.com/short", HTML: "<p>SHORTBRIDGE</p>"}

	result := e.Extract(d)
	if result.Decision() != sdk.ExtractorFallback {
		t.Fatalf("decision = %v, want fallback", result.Decision())
	}
	if d.Text != "" {
		t.Errorf("text = %q, want untouched document on fallback", d.Text)
	}
}

func TestExtractFallsBackOnBridgeFailure(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": buildStub(t)})
	d := &document.Document{URL: "https://example.com/fail", HTML: "<p>FAILBRIDGE</p>"}

	result := e.Extract(d)
	if result.Decision() != sdk.ExtractorFallback {
		t.Fatalf("decision = %v, want fallback", result.Decision())
	}
	if result.Err() == nil || !strings.Contains(result.Err().Error(), "simulated bridge failure") {
		t.Errorf("err = %v, want bridge diagnostic propagated", result.Err())
	}
}

func TestExtractFallsBackOnMissingBinary(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": "/nonexistent/defuddle-bridge"})
	d := &document.Document{URL: "https://example.com/x", HTML: "<p>x</p>"}

	result := e.Extract(d)
	if result.Decision() != sdk.ExtractorFallback {
		t.Fatalf("decision = %v, want fallback when the bridge binary is missing", result.Decision())
	}
}

func TestPreviewFallsBackBelowWordThreshold(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": buildStub(t)})
	d := &document.Document{URL: "https://example.com/short", HTML: "<p>SHORTBRIDGE</p>"}

	result := e.Preview(d)
	if result.Decision() != sdk.ExtractorFallback {
		t.Fatalf("decision = %v, want fallback", result.Decision())
	}
}

func TestPreviewSanitizesContentHTML(t *testing.T) {
	e := newExtractor(t, map[string]any{"binary": buildStub(t)})
	d := &document.Document{URL: "https://example.com/script", HTML: "<p>SCRIPTBRIDGE</p>"}

	result := e.Preview(d)
	if result.Decision() != sdk.ExtractorSuccess {
		t.Fatalf("decision = %v, want success (err: %v)", result.Decision(), result.Err())
	}
	content := result.Response().Content
	if !strings.Contains(content, "scripted") {
		t.Errorf("preview content = %q, want article text retained", content)
	}
	if strings.Contains(strings.ToLower(content), "<script") {
		t.Errorf("preview content = %q, want script tags stripped by sanitization", content)
	}
}
