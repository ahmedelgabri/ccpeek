package index

import (
	"context"
	"fmt"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

type ingestRecorder struct {
	mode           string
	claudeDir      string
	started        time.Time
	filesSeen      int
	filesChanged   int
	recordsIndexed int
	issues         []model.IngestIssue

	skippedFiles    int
	skippedRows     int
	parseFailures   int
	unresolvedLinks int
}

func newIngestRecorder(mode, claudeDir string) *ingestRecorder {
	return &ingestRecorder{
		mode:      mode,
		claudeDir: claudeDir,
		started:   time.Now().UTC(),
	}
}

func (r *ingestRecorder) SetFilesSeen(n int) {
	r.filesSeen = n
}

func (r *ingestRecorder) SetFilesChanged(n int) {
	r.filesChanged = n
}

func (r *ingestRecorder) AddIndexed(n int) {
	r.recordsIndexed += n
}

func (r *ingestRecorder) HasIssues() bool {
	return len(r.issues) > 0
}

func (r *ingestRecorder) SkippedFile(sourceType, sourcePath, detail string) {
	r.skippedFiles++
	r.addIssue("warning", "skipped_file", sourceType, sourcePath, 0, detail)
}

func (r *ingestRecorder) SkippedRow(sourceType, sourcePath string, lineNumber int, detail string) {
	r.skippedRows++
	r.addIssue("warning", "skipped_row", sourceType, sourcePath, lineNumber, detail)
}

func (r *ingestRecorder) ParseFailure(sourceType, sourcePath string, lineNumber int, detail string) {
	r.parseFailures++
	if lineNumber > 0 {
		r.skippedRows++
	} else {
		r.skippedFiles++
	}
	r.addIssue("warning", "parse_failure", sourceType, sourcePath, lineNumber, detail)
}

func (r *ingestRecorder) UnresolvedLink(sourceType, sourcePath, detail string) {
	r.unresolvedLinks++
	r.addIssue("warning", "unresolved_link", sourceType, sourcePath, 0, detail)
}

func (r *ingestRecorder) addIssue(severity, category, sourceType, sourcePath string, lineNumber int, detail string) {
	r.issues = append(r.issues, model.IngestIssue{
		Severity:   severity,
		Category:   category,
		SourceType: sourceType,
		SourcePath: sourcePath,
		LineNumber: lineNumber,
		Detail:     detail,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
}

func (r *ingestRecorder) finish(err error) (model.IngestRun, []model.IngestIssue) {
	finished := time.Now().UTC()
	status := "success"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
	} else if len(r.issues) > 0 {
		status = "partial"
	}

	run := model.IngestRun{
		Mode:            r.mode,
		Status:          status,
		ClaudeDir:       r.claudeDir,
		StartedAt:       r.started.Format(time.RFC3339),
		FinishedAt:      finished.Format(time.RFC3339),
		DurationMS:      finished.Sub(r.started).Milliseconds(),
		FilesSeen:       r.filesSeen,
		FilesChanged:    r.filesChanged,
		RecordsIndexed:  r.recordsIndexed,
		SkippedFiles:    r.skippedFiles,
		SkippedRows:     r.skippedRows,
		ParseFailures:   r.parseFailures,
		UnresolvedLinks: r.unresolvedLinks,
		WarningCount:    len(r.issues),
		ErrorMessage:    message,
	}
	return run, r.issues
}

func persistIngestRun(ctx context.Context, s *store.Store, r *ingestRecorder, err error, allowEmpty bool) error {
	if r == nil {
		return nil
	}
	if !allowEmpty && err == nil && r.filesChanged == 0 && !r.HasIssues() {
		return nil
	}

	run, issues := r.finish(err)
	if saveErr := s.SaveIngestRun(ctx, &run, issues); saveErr != nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("recording ingest diagnostics: %w", saveErr)
	}
	return err
}
