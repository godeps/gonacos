#!/bin/bash
set -e

# Build the React SPA inside pkg/web/console-ui and emit dist/, which is
# embedded into the gonacos binary via pkg/web/embed.go.

cd "$(dirname "$0")/console-ui"

if [ ! -d node_modules ]; then
    echo "Installing npm dependencies (npm ci)..."
    npm ci
fi

echo "Building React SPA (npm run build)..."
npm run build

echo "Web build complete: pkg/web/console-ui/dist/"
