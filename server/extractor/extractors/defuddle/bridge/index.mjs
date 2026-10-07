#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// defuddle-bridge: JSON-in/JSON-out wrapper around defuddle for Hister.
//
// Protocol: read a single JSON object {"url": "...", "html": "..."} from
// stdin and write the extraction result as one JSON object to stdout.
// Diagnostics go to stderr; any failure exits non-zero without stdout
// output. Input is always passed via stdin, never argv.

import { Defuddle } from 'defuddle/node';
import { parseHTML } from 'linkedom';

const MAX_INPUT_BYTES = 5 * 1024 * 1024;

// Node type constants (numeric literals avoid depending on a window global).
const TEXT_NODE = 3;
const COMMENT_NODE = 8;

// Tags that start a new line in the plain-text rendering.
const BLOCK_TAGS = new Set([
	'address', 'article', 'aside', 'blockquote', 'details', 'div', 'dd', 'dl',
	'dt', 'fieldset', 'figcaption', 'figure', 'footer', 'form', 'h1', 'h2',
	'h3', 'h4', 'h5', 'h6', 'header', 'hr', 'li', 'main', 'nav', 'ol', 'p',
	'pre', 'section', 'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'ul'
]);

function fail(message) {
	process.stderr.write(`defuddle-bridge: ${message}\n`);
	process.exit(1);
}

function readStdin(maxBytes) {
	return new Promise((resolve, reject) => {
		const chunks = [];
		let size = 0;
		process.stdin.on('data', (chunk) => {
			size += chunk.length;
			if (size > maxBytes) {
				reject(new Error(`input exceeds maximum size of ${maxBytes} bytes`));
				process.stdin.destroy();
				return;
			}
			chunks.push(chunk);
		});
		process.stdin.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
		process.stdin.on('error', reject);
	});
}

// Render an HTML fragment as block-aware plain text suitable for search
// indexing: whitespace collapses inside inline runs, block elements and <br>
// introduce line breaks, and <pre> content is preserved verbatim.
function renderText(node, out) {
	for (const child of node.childNodes ?? []) {
		if (child.nodeType === COMMENT_NODE) continue;
		if (child.nodeType === TEXT_NODE) {
			out.push(String(child.data).replace(/\s+/g, ' '));
			continue;
		}
		const tag = String(child.tagName ?? '').toLowerCase();
		if (tag === 'pre') {
			out.push('\n', String(child.textContent ?? ''), '\n');
			continue;
		}
		if (tag === 'br') {
			out.push('\n');
			continue;
		}
		renderText(child, out);
		if (BLOCK_TAGS.has(tag)) out.push('\n');
	}
}

function htmlToText(html) {
	const { document } = parseHTML(html);
	const parts = [];
	// Walk from the document node: linkedom places fragment content (which
	// defuddle's serialized content is) directly under the document, while
	// full documents nest it under html/body.
	renderText(document, parts);
	return parts
		.join('')
		.replace(/[ \t]+\n/g, '\n')
		.replace(/\n{3,}/g, '\n\n')
		.trim();
}

async function main() {
	const raw = await readStdin(MAX_INPUT_BYTES);

	let input;
	try {
		input = JSON.parse(raw);
	} catch {
		fail('stdin is not valid JSON');
	}
	if (input === null || typeof input !== 'object' || Array.isArray(input)) {
		fail('stdin must be a JSON object with "html" and optional "url" fields');
	}
	if (typeof input.html !== 'string' || input.html.length === 0) {
		fail('"html" must be a non-empty string');
	}
	if (input.url !== undefined && typeof input.url !== 'string') {
		fail('"url" must be a string when present');
	}

	// The string input path is deprecated upstream but routes through
	// defuddle's own linkedom compatibility layer (parse polyfills, URL
	// binding); the dependency is pinned, so the behavior is stable here.
	const result = await Defuddle(input.html, input.url || undefined);

	const payload = {
		title: result.title ?? '',
		text: htmlToText(result.content ?? ''),
		content_html: result.content ?? '',
		word_count: result.wordCount ?? 0,
		language: result.language ?? '',
		domain: result.domain ?? '',
		favicon: result.favicon ?? '',
		metadata: {
			author: result.author ?? '',
			published: result.published ?? '',
			site: result.site ?? '',
			description: result.description ?? '',
			image: result.image ?? ''
		}
	};
	process.stdout.write(JSON.stringify(payload) + '\n');
}

main().catch((err) => fail(err?.stack?.split('\n')[0] || String(err)));
