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
#   - "Integration (real dockerd)"      — path-filtered; would hang pending.
#   - "Fuzz (...)"                      — coordinator-starvation flake.
#   - "Node CI / Web Contract"          — page-weight flake seen during #152.
#   - "Security: Grype Container Scan"  — carries `if: github.event_name !=
#     'pull_request'`, so it ALWAYS skips on a PR. Requiring it would wedge
#     every PR permanently; a skipped check is not a passing one.
#   (There is no longer a "Security: Govulncheck" job to exclude. It duplicated
#   the required "Go CI / Govulncheck" at an older tool version, v1.2.0 against
#   v1.7.0 upstream, so it was deleted rather than left excluded. govulncheck
#   now has exactly one gate.)
#
# The two security-grype.yml jobs that ARE required below only became safe to
# require once that workflow lost its `paths:` filter. A required check must
# report on every PR shape; a path-filtered workflow produces no check run at
# all on a PR it does not match, and GitHub waits forever for a status that
# never arrives. If those jobs need to get cheaper, gate the expensive STEPS
# inside the job, never the workflow trigger.
#
# This list MUST match the live ruleset. The script updates the ruleset in
# place, so a stale list here silently REMOVES required checks the next time
# anyone runs it. Verified against ruleset 17620625 on 2026-08-19.
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

RULESET="$(
	cat <<'JSON'
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
          { "context": "Go CI / Build & Test",      "integration_id": 15368 },
          { "context": "Go CI / Lint",              "integration_id": 15368 },
          { "context": "Go CI / Govulncheck",       "integration_id": 15368 },
          { "context": "Go CI / Workflow Security", "integration_id": 15368 },
          { "context": "Go CI / Commit Message",    "integration_id": 15368 },
          { "context": "Go CI / GoReleaser Config", "integration_id": 15368 },
          { "context": "Go CI / Qlty Check",        "integration_id": 15368 },
          { "context": "Security: Secrets",         "integration_id": 15368 },
          { "context": "Dependency Review",         "integration_id": 15368 },
          { "context": "CodeQL Analysis",           "integration_id": 15368 },
          { "context": "Security: Gosec SAST",      "integration_id": 15368 },
          { "context": "Security: Grype Dependency Scan (Go + npm)", "integration_id": 15368 }
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
