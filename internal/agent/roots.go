package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Getenv abstracts os.Getenv for testability.
type Getenv func(key string) string

// ResolveRoots applies the root-discovery precedence for one adapter
// (docs/v2-plan.md §5.1):
//
//  1. explicit ccpeek config/flags (configPaths) — all of them, in order;
//  2. otherwise the agent's own env override(s), honored as the agent
//     itself would;
//  3. otherwise the platform defaults.
//
// Config roots are additive overrides: when present, env and defaults are
// not consulted, matching "explicit wins" semantics. Paths are cleaned and
// ~-expanded against home. Existence is NOT checked here — the ingest
// pipeline stats roots and reports missing ones as diagnostics, so a typo'd
// --root surfaces instead of silently falling back.
func ResolveRoots(slug canon.AgentSlug, spec RootSpec, configPaths []string, getenv Getenv, home string) []Root {
	if getenv == nil {
		getenv = os.Getenv
	}

	if len(configPaths) > 0 {
		return makeRoots(slug, configPaths, RootFromConfig, home)
	}

	for _, key := range spec.EnvVars {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			continue
		}
		paths := []string{val}
		if spec.EnvIsList {
			sep := spec.ListSep
			if sep == "" {
				sep = ","
			}
			paths = nil
			for _, p := range strings.Split(val, sep) {
				if p = strings.TrimSpace(p); p != "" {
					paths = append(paths, p)
				}
			}
		}
		if len(paths) > 0 {
			return makeRoots(slug, paths, RootFromEnv, home)
		}
	}

	return makeRoots(slug, spec.Defaults, RootFromDefault, home)
}

func makeRoots(slug canon.AgentSlug, paths []string, origin RootOrigin, home string) []Root {
	roots := make([]Root, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		p = expandHome(p, home)
		p = filepath.Clean(p)
		if p == "." || seen[p] {
			continue
		}
		seen[p] = true
		roots = append(roots, Root{Agent: slug, Path: p, Origin: origin})
	}
	return roots
}

func expandHome(path, home string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
