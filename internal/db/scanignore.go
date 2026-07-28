package db

import "fmt"

// The user's scan-ignore decisions live in user_annotations under
// agent-qualified natural keys. The format and its wildcard fallback are
// defined ONCE here, because five surfaces read or write them: the scanner
// resolving stored findings, the scan list, the Overview's active-findings
// count, the ignore toggle, and the v1 importer. Each used to spell the
// rule out for itself — two of them in SQL — so a sixth surface, or a
// change to either half, would silently revive dismissed secrets somewhere.
//
// A key is "<naturalKey>/<ruleID>/<line>", where naturalKey is already
// agent-qualified ("message/<agent>/<session>",
// "artifact/<agent>/<kind>/<name>"). Two agents can legitimately reuse an
// external session id or artifact name, so un-qualified keys would let one
// agent's scan dismiss the other's findings.
const (
	ScanFindingEntity = "scan_finding"
	ScanIgnoreKind    = "scan_ignore"
)

// ScanIgnoreKey is the exact key for one finding.
func ScanIgnoreKey(naturalKey, ruleID string, line int) string {
	return fmt.Sprintf("%s/%s/%d", naturalKey, ruleID, line)
}

// ScanIgnoreWildcardKey dismisses every finding of a rule on an entity.
// The v1 importer writes these: v1's line numbering has no v2 equivalent,
// so the decision it carries over is "this rule, on this entity".
func ScanIgnoreWildcardKey(naturalKey, ruleID string) string {
	return naturalKey + "/" + ruleID + "/*"
}

// ScanIgnoredSQL is the predicate matching a scan_findings row against
// both key forms. alias is the findings table's alias in the caller's
// query. It reads as a boolean, so it fits an EXISTS-style projection or a
// WHERE clause equally.
func ScanIgnoredSQL(alias string) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM user_annotations ua
		WHERE ua.entity_type = '%s' AND ua.kind = '%s'
		  AND ua.natural_key IN (
		    %[3]s.natural_key || '/' || %[3]s.rule_id || '/' || %[3]s.line_number,
		    %[3]s.natural_key || '/' || %[3]s.rule_id || '/*'
		  )
	)`, ScanFindingEntity, ScanIgnoreKind, alias)
}
