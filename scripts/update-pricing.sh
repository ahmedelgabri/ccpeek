#!/usr/bin/env bash
# Refresh the embedded pricing snapshot from LiteLLM's cross-provider
# pricing database (docs/v2-plan.md §5.3). Prunes the ~1.6 MB source to the
# per-token cost fields ccpeek uses, dropping entries that can't price
# a chat completion. Run from the repo root:
#
#   ./scripts/update-pricing.sh
#
# Then commit the regenerated internal/pricing/snapshot.json.
set -euo pipefail

SOURCE_URL="https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
OUT="internal/pricing/snapshot.json"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

curl -fsSL "$SOURCE_URL" -o "$tmp"

jq --arg source "$SOURCE_URL" --arg fetched_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '
  def tier($v; $threshold; $label):
    ({
      above_input_tokens: $threshold,
      input:          $v["input_cost_per_token_above_" + $label + "_tokens"],
      output:         $v["output_cost_per_token_above_" + $label + "_tokens"],
      cache_write:    $v["cache_creation_input_token_cost_above_" + $label + "_tokens"],
      cache_write_1h: $v["cache_creation_input_token_cost_above_1hr_above_" + $label + "_tokens"],
      cache_read:     $v["cache_read_input_token_cost_above_" + $label + "_tokens"]
    } | with_entries(select(.value != null)));
  {
    source: $source,
    fetched_at: $fetched_at,
    models: (
      to_entries
      | map(select(
          (.key != "sample_spec")
          and (.value | type == "object")
          and (.value.input_cost_per_token? != null)
          and (.value.output_cost_per_token? != null)
        ))
      | map({
          key: .key,
          value: ({
            input:          .value.input_cost_per_token,
            output:         .value.output_cost_per_token,
            cache_write:    .value.cache_creation_input_token_cost?,
            cache_write_1h: .value.cache_creation_input_token_cost_above_1hr?,
            cache_read:     .value.cache_read_input_token_cost?,
            tiers: ([
              tier(.value; 128000; "128k"),
              tier(.value; 200000; "200k"),
              tier(.value; 256000; "256k"),
              tier(.value; 272000; "272k"),
              tier(.value; 512000; "512k")
            ] | map(select(length > 1)))
          } | with_entries(select(.value != null and .value != [])))
        })
      | from_entries
    )
  }
' "$tmp" >"$OUT"

# Match the repo formatter so a refresh never reintroduces treefmt drift.
pnpm -C ui exec prettier --write "../$OUT" >/dev/null

count="$(jq '.models | length' "$OUT")"
size="$(wc -c <"$OUT")"
echo "wrote $OUT: $count models, $size bytes"
