package ops

import "testing"

// TestRegistryShape pins the operation surface: every op the transports
// derive from must exist with the parameters the review found missing
// from MCP (sessions.model, transcript.full, usage.model), and the read
// surface must match HTTP's endpoint set.
func TestRegistryShape(t *testing.T) {
	byName := map[string]Op{}
	for _, op := range Registry() {
		byName[op.Name] = op
	}

	want := []string{
		"sessions", "session", "transcript", "usage", "search",
		"commands", "history", "stats", "blocks", "scan", "artifacts",
		"artifact", "tools", "tool", "budget",
	}
	for _, name := range want {
		if _, found := byName[name]; !found {
			t.Errorf("registry missing op %q", name)
		}
	}
	if len(byName) != len(want) {
		t.Errorf("registry has %d ops, want %d — update the HTTP parity expectations too", len(byName), len(want))
	}

	param := func(op, name string) *Param {
		for _, p := range byName[op].Params {
			if p.Name == name {
				return &p
			}
		}
		return nil
	}
	// The drift MCP had: these must be first-class parameters everywhere.
	if param("sessions", "model") == nil {
		t.Error("sessions op lost its model filter")
	}
	if param("transcript", "full") == nil {
		t.Error("transcript op lost its full switch")
	}
	if param("usage", "model") == nil {
		t.Error("usage op lost its model filter")
	}
	for _, op := range Registry() {
		if op.Run == nil {
			t.Errorf("op %q has no executor", op.Name)
		}
		for _, p := range op.Params {
			switch p.Type {
			case "string", "integer", "boolean":
			default:
				t.Errorf("op %q param %q has unknown type %q", op.Name, p.Name, p.Type)
			}
		}
	}
}
