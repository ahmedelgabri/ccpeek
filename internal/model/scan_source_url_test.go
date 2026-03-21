package model

import "testing"

func TestSourceURLAdditionalTypes(t *testing.T) {
	tests := []struct {
		name    string
		finding ScanFinding
		want    string
	}{
		{"todo", ScanFinding{SourceType: "todo", SourceID: "todo.json#item-0"}, "/todos/todo/#item-0"},
		{"task", ScanFinding{SourceType: "task", SourceID: "task-dir#task-1"}, "/tasks/task-dir/#task-1"},
		{"file_history", ScanFinding{SourceType: "file_history", SourceID: "conv-1"}, "/file-history/conv-1/"},
		{"usage_facet", ScanFinding{SourceType: "usage_facet", SourceID: "sess-1"}, "/usage-data/sess-1/"},
		{"usage_report", ScanFinding{SourceType: "usage_report", SourceID: "report"}, "/usage-data/report/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.finding.SourceURL(); got != tt.want {
				t.Fatalf("SourceURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
