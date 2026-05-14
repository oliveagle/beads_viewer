package search

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const shortQueryDocBoost = 0.35

// ShortQueryLexicalBoost returns a literal-match boost for short queries.
// It operates on the same document text used for indexing.
func ShortQueryLexicalBoost(query string, doc string) float64 {
	if !IsShortQuery(query) {
		return 0
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" || doc == "" {
		return 0
	}
	if shortQueryMatchesDocument(needle, strings.ToLower(doc)) {
		return shortQueryDocBoost
	}
	return 0
}

// ApplyShortQueryLexicalBoost adds a literal-match boost to short-query results and re-sorts.
func ApplyShortQueryLexicalBoost(results []SearchResult, query string, docs map[string]string) []SearchResult {
	if len(results) == 0 || len(docs) == 0 {
		return results
	}
	if !IsShortQuery(query) {
		return results
	}

	boosted := false
	for i := range results {
		doc, ok := docs[results[i].IssueID]
		if !ok {
			continue
		}
		boost := ShortQueryLexicalBoost(query, doc)
		if boost == 0 {
			continue
		}
		results[i].Score += boost
		boosted = true
	}

	if boosted {
		sort.Slice(results, func(i, j int) bool {
			if results[i].Score == results[j].Score {
				return results[i].IssueID < results[j].IssueID
			}
			return results[i].Score > results[j].Score
		})
	}

	return results
}

func shortQueryMatchesDocument(query string, doc string) bool {
	queryTokens := lexicalTokens(query)
	if len(queryTokens) == 0 {
		return false
	}
	docTokens := lexicalTokens(doc)
	if len(docTokens) == 0 {
		return false
	}

	for _, queryToken := range queryTokens {
		if !hasMatchingDocumentToken(queryToken, docTokens) {
			return false
		}
	}
	return true
}

func lexicalTokens(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, s[start:])
	}
	return tokens
}

func hasMatchingDocumentToken(queryToken string, docTokens []string) bool {
	for _, docToken := range docTokens {
		if utf8.RuneCountInString(queryToken) <= 2 {
			if strings.Compare(docToken, queryToken) == 0 {
				return true
			}
			continue
		}
		if strings.HasPrefix(docToken, queryToken) {
			return true
		}
	}
	return false
}
