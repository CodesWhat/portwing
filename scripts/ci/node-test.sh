#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

npm ci
npm run check:web
