#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
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
# "Website Browser Checks" (ci-verify.yml, the job posts under its own name) is
# required too. It is safe because ci-verify.yml has no `paths:` filter on its
# pull_request trigger and the job only skips on `schedule`, never on a PR.
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
# anyone runs it. Verified against ruleset 17620625 on 2026-11-02
# (14 live contexts matched the prior list; Website Browser Checks added as the 15th).
#
# Modifying repo security settings is intentionally NOT automated by the agent —
# this script is the one human-applied step. Idempotent: creates the ruleset if
# absent, updates it in place if a ruleset of this name already exists.
#
# The payload must reproduce EVERY field the live ruleset sets, not only the
# ones this script cares about: a PUT replaces the rule parameters, so an
# omitted field resets to its default (for example
# require_extra_approval_for_unattributed_changes).
# Dry run: --print-payload prints the JSON body without calling the API, so it
# can be diffed against live with no side effects:
#   diff <(bash scripts/apply-branch-protection.sh --print-payload | jq -S .) \
#        <(gh api repos/CodesWhat/portwing/rulesets/17620625 |
#          jq -S '{name,target,enforcement,conditions,bypass_actors,rules}')
# The only difference should be a context this script adds that live lacks.
#
# Usage:
#   bash scripts/apply-branch-protection.sh            # CodesWhat/portwing
#   bash scripts/apply-branch-protection.sh --print-payload   # dry run, no API
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
        "required_reviewers": [],
        "require_code_owner_review": true,
        "dismissal_restriction": { "enabled": false, "allowed_actors": [] },
        "require_last_push_approval": true,
        "required_review_thread_resolution": false,
        "require_extra_approval_for_unattributed_changes": true,
        "allowed_merge_methods": ["merge", "squash", "rebase"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "do_not_enforce_on_create": false,
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
          { "context": "Security: Grype Dependency Scan (Go + npm)", "integration_id": 15368 },
          { "context": "Release Contract",          "integration_id": 15368 },
          { "context": "Website Browser Checks",    "integration_id": 15368 },
          { "context": "codecov/patch",             "integration_id": 254 }
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

if [ "${1:-}" = "--print-payload" ]; then
	printf '%s\n' "$RULESET"
	exit 0
fi

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
