// SPDX-License-Identifier: AGPL-3.0-or-later

// Package defuddle provides a generic article extractor backed by the
// external defuddle-bridge binary (see the bridge/ directory). The bridge
// wraps the defuddle library and speaks a JSON protocol over stdin/stdout,
// mirroring how the ytdlp extractor delegates to the yt-dlp tool.
package defuddle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/asciimoo/hister/server/extractor/sdk"
	"github.com/asciimoo/hister/server/sanitizer"
)

const (
	// minWordCount is the floor below which an extraction is considered
	// unusable and the chain should fall back to the next extractor.
	minWordCount = 10

	defaultTimeout           = 10
	defaultMaxConcurrentJobs = 2
)

// DefuddleExtractor extracts article content and metadata from any web page
// using the defuddle-bridge binary.
type DefuddleExtractor struct {
	cfg      *sdk.Config
	jobSlots chan struct{}
}

// Compile-time assertion that the extractor satisfies the SDK contract.
var _ sdk.Extractor = (*DefuddleExtractor)(nil)

func (e *DefuddleExtractor) Name() string {
	return "Defuddle"
}

func (e *DefuddleExtractor) Description() string {
	return "Extracts the main article content from any web page using the defuddle-bridge tool, removing navigation, boilerplate, and other clutter."
}

func (e *DefuddleExtractor) Capabilities() sdk.Capabilities {
	return sdk.Capabilities{Extract: true, Preview: true}
}

// GetConfig returns the extractor configuration. Defuddle is disabled by
// default because it requires the external defuddle-bridge binary.
func (e *DefuddleExtractor) GetConfig() *sdk.Config {
	if e.cfg == nil {
		return &sdk.Config{
			Enable: false,
			Options: map[string]any{
				"binary":              "defuddle-bridge",
				"timeout":             defaultTimeout,
				"max_concurrent_jobs": defaultMaxConcurrentJobs,
			},
		}
	}
	return e.cfg
}

func (e *DefuddleExtractor) SetConfig(c *sdk.Config) error {
	for k := range c.Options {
		switch k {
		case "binary", "timeout", "max_concurrent_jobs":
		default:
			return fmt.Errorf("unknown option %q", k)
		}
	}
	maxJobs, err := maxConcurrentJobs(c.Options)
	if err != nil {
		return err
	}
	var jobSlots chan struct{}
	if maxJobs > 0 {
		jobSlots = make(chan struct{}, maxJobs)
	}
	e.cfg = c
	e.jobSlots = jobSlots
	return nil
}

func maxConcurrentJobs(options map[string]any) (int, error) {
	value, ok := options["max_concurrent_jobs"]
	if !ok {
		return defaultMaxConcurrentJobs, nil
	}
	var jobs int
	switch value := value.(type) {
	case int:
		jobs = value
	case float64:
		jobs = int(value)
		if float64(jobs) != value {
			return 0, errors.New("max_concurrent_jobs must be an integer")
		}
	default:
		return 0, errors.New("max_concurrent_jobs must be an integer")
	}
	if jobs < 0 {
		return 0, errors.New("max_concurrent_jobs must not be negative")
	}
	return jobs, nil
}

func (e *DefuddleExtractor) runJob(ctx context.Context, job func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	jobSlots := e.jobSlots
	if jobSlots == nil {
		return job()
	}
	select {
	case jobSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-jobSlots }()
	return job()
}

func (e *DefuddleExtractor) binary() string {
	if b, ok := e.GetConfig().Options["binary"].(string); ok && b != "" {
		return b
	}
	return "defuddle-bridge"
}

func (e *DefuddleExtractor) timeout() time.Duration {
	switch v := e.GetConfig().Options["timeout"].(type) {
	case int:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v) * time.Second
	}
	return defaultTimeout * time.Second
}

// Match reports whether this extractor should handle the given document.
// Defuddle is a generic extractor and matches every document.
func (e *DefuddleExtractor) Match(_ *sdk.Document) bool {
	return true
}

// bridgeInput is the JSON payload sent to the bridge on stdin.
type bridgeInput struct {
	URL  string `json:"url"`
	HTML string `json:"html"`
}

// bridgeOutput is the JSON payload the bridge writes to stdout.
type bridgeOutput struct {
	Title       string `json:"title"`
	Text        string `json:"text"`
	ContentHTML string `json:"content_html"`
	WordCount   int    `json:"word_count"`
	Language    string `json:"language"`
	Domain      string `json:"domain"`
	Favicon     string `json:"favicon"`
	Metadata    struct {
		Author      string `json:"author"`
		Published   string `json:"published"`
		Site        string `json:"site"`
		Description string `json:"description"`
		Image       string `json:"image"`
	} `json:"metadata"`
}

// runBridge executes the defuddle-bridge binary with the page HTML on stdin
// and decodes its JSON response.
func (e *DefuddleExtractor) runBridge(ctx context.Context, d *sdk.Document) (bridgeOutput, error) {
	payload, err := json.Marshal(bridgeInput{URL: d.URL, HTML: d.HTML})
	if err != nil {
		return bridgeOutput{}, fmt.Errorf("defuddle-bridge input: %w", err)
	}

	var out bridgeOutput
	var stdout, stderr bytes.Buffer
	runErr := e.runJob(ctx, func() error {
		ctx, cancel := context.WithTimeout(ctx, e.timeout())
		defer cancel()

		// #nosec G204 -- the binary path is admin-configured, the input
		// travels over stdin, never argv.
		cmd := exec.CommandContext(ctx, e.binary())
		cmd.Stdin = bytes.NewReader(payload)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		log.Debug().Str("URL", d.URL).Str("binary", e.binary()).Msg("defuddle-bridge executing")
		return cmd.Run()
	})
	if runErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("defuddle-bridge failed: %w: %s", runErr, msg)
		}
		return out, fmt.Errorf("defuddle-bridge failed: %w", runErr)
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return out, fmt.Errorf("defuddle-bridge output: %w", err)
	}
	return out, nil
}

func (e *DefuddleExtractor) Extract(d *sdk.Document) sdk.ExtractResult {
	return e.ExtractContext(context.Background(), d)
}

// ExtractContext runs the bridge and cancels it when ctx is canceled.
func (e *DefuddleExtractor) ExtractContext(ctx context.Context, d *sdk.Document) sdk.ExtractResult {
	out, err := e.runBridge(ctx, d)
	if err != nil {
		if ctx.Err() != nil {
			return sdk.AbortExtraction(ctx.Err())
		}
		return sdk.ExtractFallback(err)
	}
	if out.WordCount < minWordCount {
		return sdk.ExtractFallback(fmt.Errorf("defuddle-bridge returned only %d words", out.WordCount))
	}

	if out.Title != "" {
		d.Title = out.Title
	}
	d.Text = out.Text
	if out.Favicon != "" {
		d.SetFaviconURL(out.Favicon)
	}
	// Recognised preview panel keys, mirroring the readability extractor.
	if d.Metadata == nil {
		d.Metadata = make(map[string]any)
	}
	set := func(k, v string) {
		if v != "" {
			d.Metadata[k] = v
		}
	}
	set("author", out.Metadata.Author)
	set("published", out.Metadata.Published)
	set("site_name", out.Metadata.Site)
	set("description", out.Metadata.Description)
	set("image", out.Metadata.Image)
	set("language", out.Language)

	if err := ctx.Err(); err != nil {
		return sdk.AbortExtraction(err)
	}
	return sdk.Extracted()
}

func (e *DefuddleExtractor) Preview(d *sdk.Document) sdk.PreviewResult {
	return e.PreviewContext(context.Background(), d)
}

// PreviewContext renders the extracted content as sanitized HTML and cancels
// the bridge when ctx is canceled.
func (e *DefuddleExtractor) PreviewContext(ctx context.Context, d *sdk.Document) sdk.PreviewResult {
	out, err := e.runBridge(ctx, d)
	if err != nil {
		if ctx.Err() != nil {
			return sdk.AbortPreview(ctx.Err())
		}
		return sdk.PreviewFallback(err)
	}
	if out.ContentHTML == "" {
		return sdk.PreviewFallback(errors.New("defuddle-bridge returned no content"))
	}
	return sdk.Previewed(sdk.PreviewResponse{
		Content: sanitizer.SanitizeHTML(out.ContentHTML),
	})
}
