package agent

import (
	"reflect"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

const testHome = "/home/u"

func env(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func paths(roots []Root) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = r.Path
	}
	return out
}

func TestResolveRootsDefaults(t *testing.T) {
	spec := RootSpec{
		EnvVars:  []string{"PI_CODING_AGENT_DIR"},
		Defaults: []string{"~/.pi/agent"},
	}
	roots := ResolveRoots(canon.AgentSlug("pi"), spec, nil, env(nil), testHome)
	if want := []string{"/home/u/.pi/agent"}; !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
	if roots[0].Origin != RootFromDefault {
		t.Fatalf("origin = %q, want default", roots[0].Origin)
	}
	if roots[0].Agent != "pi" {
		t.Fatalf("agent = %q, want pi", roots[0].Agent)
	}
}

func TestResolveRootsEnvOverride(t *testing.T) {
	spec := RootSpec{
		EnvVars:  []string{"PI_CODING_AGENT_DIR"},
		Defaults: []string{"~/.pi/agent"},
	}
	roots := ResolveRoots("pi", spec,
		nil,
		env(map[string]string{"PI_CODING_AGENT_DIR": "/data/pi"}),
		testHome)
	if want := []string{"/data/pi"}; !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
	if roots[0].Origin != RootFromEnv {
		t.Fatalf("origin = %q, want env", roots[0].Origin)
	}
}

func TestResolveRootsEnvList(t *testing.T) {
	spec := RootSpec{
		EnvVars:   []string{"OPENCODE_DATA_DIR"},
		EnvIsList: true,
		Defaults:  []string{"~/.local/share/opencode"},
	}
	roots := ResolveRoots("opencode", spec,
		nil,
		env(map[string]string{"OPENCODE_DATA_DIR": "/a/opencode, ~/oc-archive ,"}),
		testHome)
	want := []string{"/a/opencode", "/home/u/oc-archive"}
	if !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
}

func TestResolveRootsConfigWins(t *testing.T) {
	spec := RootSpec{
		EnvVars:  []string{"CLAUDE_CONFIG_DIR"},
		Defaults: []string{"~/.claude"},
	}
	roots := ResolveRoots("claude-code", spec,
		[]string{"~/backup/claude", "/mnt/claude-live"},
		env(map[string]string{"CLAUDE_CONFIG_DIR": "/ignored"}),
		testHome)
	want := []string{"/home/u/backup/claude", "/mnt/claude-live"}
	if !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
	for _, r := range roots {
		if r.Origin != RootFromConfig {
			t.Fatalf("origin = %q, want config", r.Origin)
		}
	}
}

func TestResolveRootsEmptyEnvFallsThrough(t *testing.T) {
	spec := RootSpec{
		EnvVars:  []string{"CODEX_HOME"},
		Defaults: []string{"~/.codex"},
	}
	roots := ResolveRoots("codex", spec,
		nil,
		env(map[string]string{"CODEX_HOME": "   "}),
		testHome)
	if want := []string{"/home/u/.codex"}; !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
}

func TestResolveRootsDedupes(t *testing.T) {
	spec := RootSpec{Defaults: []string{"~/.claude"}}
	roots := ResolveRoots("claude-code", spec,
		[]string{"/x/claude", "/x/claude/", "/x/./claude"},
		env(nil), testHome)
	if want := []string{"/x/claude"}; !reflect.DeepEqual(paths(roots), want) {
		t.Fatalf("paths = %v, want %v", paths(roots), want)
	}
}
