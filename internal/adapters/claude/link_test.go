package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func ruleFor(t *testing.T, kind canon.ArtifactKind) canon.LinkRule {
	t.Helper()
	for _, r := range (&Adapter{}).LinkRules() {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatalf("no link rule for %s", kind)
	return canon.LinkRule{}
}

// storedContent is what the store keeps for an artifact: the file's bytes
// bounded at canon.ArtifactContentLimit (db.Writer.WriteArtifact), which is
// also what the rule engine hands to ArtifactKey.
func storedContent(s string) string {
	out, _ := canon.TruncateArtifactContent(s)
	return out
}

func exitPlanCall(plan string) canon.LinkToolCall {
	in, err := json.Marshal(map[string]string{"plan": plan})
	if err != nil {
		panic(err)
	}
	return canon.LinkToolCall{Name: "ExitPlanMode", InputJSON: string(in)}
}

// A plan's ONLY provenance is its text — no session id lands in its file
// name or metadata. The store bounds artifact content at 1 MiB while tool
// input is kept whole, so a plan over the limit used to compare a truncated
// artifact against an untruncated ExitPlanMode input: the keys never
// matched and the plan could never link to the session that approved it.
func TestPlanRuleMatchesOverLimitPlans(t *testing.T) {
	rule := ruleFor(t, canon.ArtifactPlan)

	// Well past the limit, and with a distinct tail so a truncation that
	// silently kept everything would still show up.
	body := "# Rate limiting plan\n\n" +
		strings.Repeat("- step with enough text to matter\n", 40000) +
		"\n## Done\n"
	if len(body) <= canon.ArtifactContentLimit {
		t.Fatalf("fixture plan is %d bytes, need more than the %d limit",
			len(body), canon.ArtifactContentLimit)
	}

	artKey, ok := rule.ArtifactKey(canon.LinkArtifact{
		Name: "rate-limit-plan.md", Content: storedContent(body),
	})
	if !ok {
		t.Fatal("artifact key not derived from a truncated plan")
	}
	callKey, ok := rule.CallKey(exitPlanCall(body))
	if !ok {
		t.Fatal("call key not derived from an over-limit ExitPlanMode input")
	}
	if artKey != callKey {
		t.Errorf("keys differ for the same plan:\n artifact ...%q\n call     ...%q",
			tail(artKey), tail(callKey))
	}
	if !strings.HasSuffix(artKey, strings.TrimSpace(canon.ArtifactTruncationMarker)) {
		t.Errorf("artifact key does not end in the truncation marker: ...%q", tail(artKey))
	}

	// The plan file on disk and the tool input differ in trailing
	// whitespace and final newlines — that is what planKey normalizes —
	// and the match must survive it at the limit too.
	callKey, ok = rule.CallKey(exitPlanCall(body + "\n\n  \n"))
	if !ok || callKey != artKey {
		t.Errorf("trailing whitespace broke the over-limit match (ok=%v)", ok)
	}
}

// Plans under the limit are untouched: truncation is a no-op there, so the
// key is still the plain normalized markdown.
func TestPlanRuleUnchangedForOrdinaryPlans(t *testing.T) {
	rule := ruleFor(t, canon.ArtifactPlan)
	const body = "# Small plan\n\n- one step\n- two steps\n"

	artKey, ok := rule.ArtifactKey(canon.LinkArtifact{Name: "p.md", Content: body})
	if !ok {
		t.Fatal("artifact key not derived")
	}
	callKey, ok := rule.CallKey(exitPlanCall(body + "  \n"))
	if !ok || callKey != artKey {
		t.Fatalf("keys differ: %q vs %q", artKey, callKey)
	}
	if strings.Contains(artKey, "truncated") {
		t.Errorf("a short plan picked up a truncation marker: %q", artKey)
	}
	if _, ok := rule.CallKey(exitPlanCall("   ")); ok {
		t.Error("a blank plan produced a join key")
	}
}

func tail(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[len(s)-60:]
}
