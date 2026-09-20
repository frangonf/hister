// SPDX-License-Identifier: AGPL-3.0-or-later
// Protocol smoke tests for defuddle-bridge.
//
// Runs `node index.mjs` as a child process and asserts on the JSON protocol:
// success shape, clutter removal, and failure semantics (bad JSON, missing or
// invalid fields, oversized input). Exits non-zero on the first failure set.

import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const bridge = path.join(path.dirname(fileURLToPath(import.meta.url)), 'index.mjs');
const MAX_INPUT_BYTES = 5 * 1024 * 1024;

// Synthetic, anonymized article fixture with nav/footer clutter.
const FIXTURE = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Example Article Title</title>
<meta property="og:title" content="Example Article Title">
<meta name="author" content="Author Name">
<meta property="article:published_time" content="2026-01-15T10:00:00Z">
</head>
<body>
<nav><a href="/">Home</a><a href="/skip">SkipNavLabel</a></nav>
<article>
<h1>Example Article Title</h1>
<p>This is the main article paragraph and it has enough words for the content
scoring heuristics to select the article element without any trouble at all.</p>
<p>The second paragraph continues the main article body and includes
<a href="https://example.com/x">an inline link</a> for text coverage.</p>
</article>
<footer>Copyright Filler Text</footer>
</body>
</html>`;

let failures = 0;

function check(name, condition, detail) {
	if (condition) {
		console.log(`ok - ${name}`);
	} else {
		failures++;
		console.error(`FAIL - ${name}${detail ? `: ${detail}` : ''}`);
	}
}

function runBridge(input) {
	return spawnSync(process.execPath, [bridge], {
		input,
		encoding: 'utf8',
		maxBuffer: 16 * 1024 * 1024,
	});
}

// 1. Success shape and content selection.
const ok = runBridge(JSON.stringify({ url: 'https://example.com/article', html: FIXTURE }));
check('success: exit code 0', ok.status === 0, `stderr: ${ok.stderr}`);
check('success: stderr empty on success', ok.stderr === '', ok.stderr);
let out = null;
try {
	out = JSON.parse(ok.stdout);
} catch (e) {
	check('success: stdout is valid JSON', false, `${e}\n${ok.stdout?.slice(0, 200)}`);
}
if (out) {
	check('success: title', out.title === 'Example Article Title', `got ${JSON.stringify(out.title)}`);
	check('success: word_count is a positive number', Number.isInteger(out.word_count) && out.word_count >= 20, `got ${JSON.stringify(out.word_count)}`);
	check('success: text contains main content', typeof out.text === 'string' && out.text.includes('main article paragraph'), `got ${JSON.stringify(out.text?.slice(0, 200))}`);
	check('success: nav clutter removed from text', !out.text.includes('SkipNavLabel'), `got ${JSON.stringify(out.text?.slice(0, 200))}`);
	check('success: content_html is non-empty HTML', typeof out.content_html === 'string' && out.content_html.length > 0);
	check('success: language detected', out.language === 'en', `got ${JSON.stringify(out.language)}`);
	check('success: metadata.author', out.metadata?.author === 'Author Name', `got ${JSON.stringify(out.metadata?.author)}`);
	check('success: metadata.published', out.metadata?.published === '2026-01-15T10:00:00.000Z' || out.metadata?.published === '2026-01-15T10:00:00Z', `got ${JSON.stringify(out.metadata?.published)}`);
	check('success: domain', out.domain === 'example.com', `got ${JSON.stringify(out.domain)}`);
}

// 2. Invalid JSON on stdin.
const badJson = runBridge('this is not json');
check('bad json: non-zero exit', badJson.status !== 0);
check('bad json: no stdout payload', badJson.stdout === '', badJson.stdout);
check('bad json: stderr diagnostic', badJson.stderr.length > 0);

// 3. Missing html field.
const noHtml = runBridge(JSON.stringify({ url: 'https://example.com/' }));
check('missing html: non-zero exit', noHtml.status !== 0);
check('missing html: stderr diagnostic', noHtml.stderr.length > 0);

// 4. html field of wrong type.
const wrongType = runBridge(JSON.stringify({ html: 42 }));
check('wrong html type: non-zero exit', wrongType.status !== 0);

// 5. Oversized input beyond the cap.
const bigHtml = 'x'.repeat(MAX_INPUT_BYTES + 1024);
const oversized = runBridge(JSON.stringify({ html: bigHtml }));
check('oversized: non-zero exit', oversized.status !== 0);
check('oversized: mentions size in stderr', /size|large|exceed/i.test(oversized.stderr), oversized.stderr);

if (failures) {
	console.error(`\n${failures} check(s) failed`);
	process.exit(1);
}
console.log('\nall checks passed');
