// SPDX-License-Identifier: AGPL-3.0-or-later

// Command stub is a test double for the defuddle-bridge binary. It speaks
// the same stdin/stdout JSON protocol and selects its behavior from markers
// embedded in the input HTML, so tests stay hermetic and cross-platform
// without depending on node or the real bridge:
//
//	FAILBRIDGE   emit a diagnostic on stderr and exit 1
//	SHORTBRIDGE  emit a success payload with word_count below the
//	             extractor's fallback threshold
//	SCRIPTBRIDGE emit a success payload whose content_html carries a script
//	             tag, to exercise preview sanitization
//	anything else emit a full success payload
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type bridgeInput struct {
	URL  string `json:"url"`
	HTML string `json:"html"`
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stub: read stdin: %v\n", err)
		os.Exit(1)
	}
	var in bridgeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintf(os.Stderr, "stub: invalid JSON: %v\n", err)
		os.Exit(1)
	}

	var payload string
	switch {
	case strings.Contains(in.HTML, "FAILBRIDGE"):
		fmt.Fprintln(os.Stderr, "stub: simulated bridge failure")
		os.Exit(1)
	case strings.Contains(in.HTML, "SHORTBRIDGE"):
		payload = `{"title":"Short","text":"tiny","content_html":"<p>tiny</p>","word_count":3,` +
			`"language":"en","domain":"example.com","favicon":"","metadata":{"author":"","published":"","site":"","description":"","image":""}}`
	case strings.Contains(in.HTML, "SCRIPTBRIDGE"):
		payload = `{"title":"Script","text":"scripted","content_html":"<p>scripted</p><script>alert(1)</script>",` +
			`"word_count":20,"language":"en","domain":"example.com","favicon":"","metadata":{"author":"","published":"","site":"","description":"","image":""}}`
	default:
		payload = `{"title":"Stub Title","text":"stub article text","content_html":"<p>stub article text</p>",` +
			`"word_count":42,"language":"en","domain":"example.com","favicon":"https://example.com/favicon.ico",` +
			`"metadata":{"author":"Stub Author","published":"2026-01-15T10:00:00Z","site":"Example",` +
			`"description":"Stub description","image":"https://example.com/img.png"}}`
	}
	fmt.Println(payload)
}
