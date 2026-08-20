#!/usr/bin/env bash
# Apply (or update) the "Protect Renovate branches" ruleset on the portwing repo.
#
# deps.md: never delete Renovate's branches or close its PRs as cleanup — they
# are the engine's working state, and deleting one makes Renovate rebuild the
# update from scratch. This ruleset makes that rule enforceable instead of
# advisory, so a tidy-up sweep by a human or an agent cannot quietly undo it.
#
# Two design points, both load-bearing:
#
#   1. `deletion` ONLY. Never add `non_fast_forward` here. Renovate force-pushes
#      its own branches by design, so a non-fast-forward rule hardens nothing
#      and just breaks the integration. This is not hypothetical: drydock's
#      ruleset 20923106 folded `l10n_crowdin` in with `dev/*` for tidiness,
#      Crowdin force-pushes that branch, and every sync from 2026-08-16 failed
#      with `GH013: Cannot force-push to this branch` while 22 files of
#      translations sat undelivered. rulesets.md calls this out by name as the
#      mistake to avoid.
#
#   2. Renovate itself is a bypass actor. This is the one deliberate difference
#      from drydock's "Protect the Crowdin branch" (21047692), which carries no
#      bypass actors. Crowdin only ever force-pushes its branch; Renovate also
#      PRUNES its own stale branches once an update lands or goes obsolete, so a
#      zero-bypass rule would fight the owning bot — the same failure shape as
#      point 1, one step removed. Bypass is ruleset-scoped, and this ruleset
#      covers only `refs/heads/renovate/**`, so the grant cannot leak onto
#      `dev/**`, `main`, or the release tags. That containment is exactly what
#      made a bypass actor the wrong answer on drydock 20923106, which spanned
#      four branch patterns.
#
# Renovate is org app_id 2740 (`gh api orgs/CodesWhat/installations`). If the
# ruleset ever stops letting Renovate prune, re-check that ID first.
#
# Modifying repo security settings is intentionally NOT automated by the agent —
# this script is a human-applied step, same as apply-branch-protection.sh.
# Idempotent: creates the ruleset if absent, updates it in place if a ruleset of
# this name already exists.
#
# Usage:
#   bash scripts/apply-renovate-branch-protection.sh
set -euo pipefail

REPO="${REPO:-CodesWhat/portwing}"
NAME="Protect Renovate branches"
RENOVATE_APP_ID="${RENOVATE_APP_ID:-2740}"

RULESET="$(
	cat <<JSON
{
  "name": "$NAME",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [
    {
      "actor_id": $RENOVATE_APP_ID,
      "actor_type": "Integration",
      "bypass_mode": "always"
    }
  ],
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/renovate/**"],
      "exclude": []
    }
  },
  "rules": [
    { "type": "deletion" }
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
  refs: .conditions.ref_name.include,
  rules: [.rules[].type],
  bypass: [.bypass_actors[] | "\(.actor_type):\(.actor_id):\(.bypass_mode)"]
}'
