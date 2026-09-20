#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Build the defuddle-bridge executable into dist/.
#
# Preferred: bun compile → a fully standalone binary with no runtime
# dependency. Fallback: esbuild bundles to a single CommonJS script with a
# node shebang, which requires Node.js at run time. Both artifacts speak the
# same stdin/stdout JSON protocol.

set -euo pipefail
cd "$(dirname "$0")"

OUT="dist/defuddle-bridge"
mkdir -p dist

if command -v bun >/dev/null 2>&1; then
	bun build index.mjs --compile --outfile "$OUT"
	echo "built standalone binary: $OUT"
else
	# The extensionless output resolves its module type from the nearest
	# package.json ("type": "module" here), so mark dist/ as CommonJS.
	echo '{"type":"commonjs"}' > dist/package.json
	npx --no-install esbuild index.mjs \
		--bundle \
		--platform=node \
		--format=cjs \
		--target=node20 \
		--outfile="$OUT"
	chmod +x "$OUT"
	echo "built node script: $OUT (requires node >= 20)"
fi
