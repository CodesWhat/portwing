#!/usr/bin/env bash
# Apply (or update) the "Main branch protection" ruleset on the portwing repo.
#
# Branch protection as code. This ruleset mirrors drydock's posture and is tuned
# to score at the top tier of the OpenSSF Scorecard Branch-Protection check:
#   - 2 required approvals, code-owner review, dismiss-stale-on-push
#   - require approval of the most recent push (require_last_push_approval)
#   - strict (up-to-date) required status checks
#   - force-push and deletion blocked; no admin bypass (empty bypass_actors)
#   - CodeQL code-scanning gate (high+ alerts / errors block merge)
#
# Required status checks are limited to the CI jobs that run on EVERY PR to main
# and are not flaky. Deliberately excluded:
#   - "🐋 Integration (real dockerd)" — path-filtered; would hang pending.
#   - "🔀 Go Fuzz (...)"            — subject to the coordinator-starvation flake.
#   - "📦 Dependency Review"        — informational (org Dependency Graph is off).
#
# Modifying repo security settings is intentionally NOT automated by the agent —
# this script is the one human-applied step. Idempotent: creates the ruleset if
# absent, updates it in place if a ruleset of this name already exists.
#
# Usage:
#   bash scripts/apply-branch-protection.sh            # CodesWhat/portwing
#   REPO=owner/name bash scripts/apply-branch-protection.sh
set -euo pipefail

REPO="${REPO:-CodesWhat/portwing}"
NAME="Main branch protection"

RULESET="$(cat <<'JSON'
{
  "name": "Main branch protection",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "bypass_actors": [],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 2,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": true,
        "required_review_thread_resolution": false,
        "allowed_merge_methods": ["merge", "squash", "rebase"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "🏗️ Build & Test",    "integration_id": 15368 },
          { "context": "🧹 Lint",             "integration_id": 15368 },
          { "context": "🔍 Govulncheck",      "integration_id": 15368 },
          { "context": "🔒 Workflow Security", "integration_id": 15368 },
          { "context": "💬 Commit Message",   "integration_id": 15368 },
          { "context": "📦 GoReleaser Config", "integration_id": 15368 }
        ]
      }
    },
    {
      "type": "code_scanning",
      "parameters": {
        "code_scanning_tools": [
          { "tool": "CodeQL", "security_alerts_threshold": "high_or_higher", "alerts_threshold": "errors" }
        ]
      }
    }
  ]
}
JSON
)"

existing_id="$(gh api "repos/$REPO/rulesets" \
  --jq ".[] | select(.name == \"$NAME\") | .id" 2>/dev/null || true)"

if [ -n "$existing_id" ]; then
  echo "→ Updating existing ruleset #$existing_id on $REPO ..."
  printf '%s' "$RULESET" | gh api --method PUT "repos/$REPO/rulesets/$existing_id" --input - >/dev/null
else
  echo "→ Creating ruleset on $REPO ..."
  printf '%s' "$RULESET" | gh api --method POST "repos/$REPO/rulesets" --input - >/dev/null
fi

id="$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name == \"$NAME\") | .id")"
echo "✓ Applied. Effective ruleset:"
gh api "repos/$REPO/rulesets/$id" --jq '{
  id, name, enforcement,
  rules: [.rules[].type],
  required_checks: [.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context]
}'
