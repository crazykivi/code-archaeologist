package analyzer

import "testing"

func TestParseDecisionsValid(t *testing.T) {
	input := "```json\n[{\"title\":\"Переход на PostgreSQL\",\"decision\":\"Решили использовать PostgreSQL вместо MySQL\"}]\n```"

	decisions, err := ParseDecisions(input)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}

	if decisions[0].Title != "Переход на PostgreSQL" {
		t.Fatalf("unexpected title: %s", decisions[0].Title)
	}

	if decisions[0].Decision != "Решили использовать PostgreSQL вместо MySQL" {
		t.Fatalf("unexpected decision: %s", decisions[0].Decision)
	}
}

func TestParseDecisionsInvalid(t *testing.T) {
	_, err := ParseDecisions("no json here")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}
