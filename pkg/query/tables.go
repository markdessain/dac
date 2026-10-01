package query

import (
	"regexp"
	"sort"
	"strings"
)

var (
	// ctePattern matches CTE names in "name AS (" or "name AS  (".
	// It deliberately does NOT match table aliases like "tbl AS alias"
	// because aliases are not followed by an opening parenthesis.
	ctePattern = regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s+AS\s*\(`)

	// tableRefPattern matches table references after FROM or any JOIN keyword.
	// Handles:
	//   FROM table
	//   FROM schema.table
	//   FROM catalog.schema.table
	//   INNER JOIN table
	//   LEFT OUTER JOIN table
	// Does NOT match subqueries: "FROM (SELECT ...)" stops at the '('.
	tableRefPattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)*)\b`)
)

// insideExtract reports whether the FROM keyword at fromPos is inside an
// EXTRACT(part FROM expr) expression. It scans backward counting parentheses;
// if the nearest unmatched '(' is preceded by the word EXTRACT, it returns true.
func insideExtract(sql string, fromPos int) bool {
	depth := 0
	for i := fromPos - 1; i >= 0; i-- {
		switch sql[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				// Found the unmatched '('. Check if preceding word is EXTRACT.
				j := i - 1
				for j >= 0 && (sql[j] == ' ' || sql[j] == '\t') {
					j--
				}
				if j >= 6 {
					word := strings.ToLower(sql[j-6 : j+1])
					if word == "extract" {
						return true
					}
				}
				return false
			}
			depth--
		}
	}
	return false
}

// ExtractBaseTables extracts the underlying table names referenced by a SQL
// query. It finds every table that appears after FROM or JOIN, then filters
// out any names that belong to CTEs (WITH ... AS (...)).
//
// The approach is intentionally lightweight (regex-based). It works well for
// standard SELECT statements with explicit JOINs. Comma-separated tables in
// FROM (deprecated ANSI-89 style) are not expanded.
func ExtractBaseTables(sql string) []string {
	// Normalise whitespace so regexes don't have to worry about newlines.
	sql = strings.Join(strings.Fields(sql), " ")

	// 1. Collect CTE names so we can exclude them later.
	cteMatches := ctePattern.FindAllStringSubmatch(sql, -1)
	cteNames := make(map[string]bool, len(cteMatches))
	for _, m := range cteMatches {
		cteNames[strings.ToLower(m[1])] = true
	}

	// 2. Collect every table-like reference after FROM or JOIN.
	// This naturally recurses into subqueries because subqueries also contain
	// FROM/JOIN clauses.
	tableMatches := tableRefPattern.FindAllStringSubmatchIndex(sql, -1)
	baseTables := make(map[string]bool)
	for _, m := range tableMatches {
		// m[0], m[1] = full match; m[2], m[3] = capture group.
		name := strings.ToLower(sql[m[2]:m[3]])
		if cteNames[name] {
			continue
		}
		// Skip EXTRACT(part FROM expr) — 'FROM' here is not a table reference.
		if strings.EqualFold(sql[m[0]:m[0]+4], "FROM") && insideExtract(sql, m[0]) {
			continue
		}
		baseTables[name] = true
	}

	// 3. Return deterministic order.
	if len(baseTables) == 0 {
		return nil
	}
	result := make([]string, 0, len(baseTables))
	for t := range baseTables {
		result = append(result, t)
	}
	sort.Strings(result)

	return result
}
