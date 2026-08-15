#!/usr/bin/env bash
set -euo pipefail

npm ci
npm run check:web
