package search

import (
	"strings"
	"testing"
)

func sameSearchID(got, want string) bool {
	return strings.Compare(got, want) == 0
}

func TestShortQueryLexicalBoost(t *testing.T) {
	doc := "Performance benchmarks for graph rendering"
	if boost := ShortQueryLexicalBoost("benchmarks", doc); boost <= 0 {
		t.Fatalf("expected boost for literal short query match")
	}
	if boost := ShortQueryLexicalBoost("bench", doc); boost <= 0 {
		t.Fatalf("expected boost for short query token prefix")
	}
	if boost := ShortQueryLexicalBoost("graph render", doc); boost <= 0 {
		t.Fatalf("expected boost when all short-query tokens match prefixes")
	}
	if boost := ShortQueryLexicalBoost("go", "Ongoing work on rendering"); boost != 0 {
		t.Fatalf("expected no boost for short query inside a larger token")
	}
	if boost := ShortQueryLexicalBoost("ui", "Build import workflow"); boost != 0 {
		t.Fatalf("expected no boost for short query inside a larger token")
	}
	if boost := ShortQueryLexicalBoost("界", "世界"); boost != 0 {
		t.Fatalf("expected no boost for one-rune query inside a larger token")
	}
	if boost := ShortQueryLexicalBoost("long descriptive query about rendering performance", doc); boost != 0 {
		t.Fatalf("expected no boost for long query")
	}
}

func TestApplyShortQueryLexicalBoostResorts(t *testing.T) {
	results := []SearchResult{
		{IssueID: "a", Score: 0.2},
		{IssueID: "b", Score: 0.5},
	}
	docs := map[string]string{
		"a": "benchmarks",
		"b": "unrelated",
	}
	updated := ApplyShortQueryLexicalBoost(results, "benchmarks", docs)
	if !sameSearchID(updated[0].IssueID, "a") {
		t.Fatalf("expected boosted match to rank first, got %s", updated[0].IssueID)
	}
}
