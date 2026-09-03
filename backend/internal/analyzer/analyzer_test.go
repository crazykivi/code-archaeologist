package analyzer

import (
	"strings"
	"testing"
)

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

func TestIsSupportedReportType(t *testing.T) {
	valid := []string{"", "decisions", "architecture", "tech_debt", "team", "DECISIONS", " Tech_Debt "}
	for _, v := range valid {
		if !IsSupportedReportType(v) {
			t.Errorf("IsSupportedReportType(%q) = false, want true", v)
		}
	}

	invalid := []string{"unknown", "arch!", "дроп-таблицу"}
	for _, v := range invalid {
		if IsSupportedReportType(v) {
			t.Errorf("IsSupportedReportType(%q) = true, want false", v)
		}
	}
}

func TestNormalizeReportType(t *testing.T) {
	if got := NormalizeReportType(""); got != ReportDecisions {
		t.Errorf("empty report type: got %q, want %q", got, ReportDecisions)
	}
	if got := NormalizeReportType("  ARCHITECTURE "); got != ReportArchitecture {
		t.Errorf("normalize: got %q, want %q", got, ReportArchitecture)
	}
}

func TestRenderReportHeaders(t *testing.T) {
	p := Params{Source: "demo", SourceType: "github", ReportType: ReportTechDebt}

	tests := []struct {
		name     string
		report   string
		contains []string
	}{
		{"decisions title", ReportDecisions, []string{"# История принятия решений", "Ключевые решения"}},
		{"architecture title", ReportArchitecture, []string{"# Эволюция архитектуры", "Архитектурные изменения"}},
		{"tech debt title", ReportTechDebt, []string{"# Технический долг в истории", "Записи о долге"}},
		{"team title", ReportTeam, []string{"# Анализ команды и вклада", "Наблюдения"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := p
			params.ReportType = tt.report
			out := renderReport(params, "Обзор", nil, 0)
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("renderReport(%s) missing %q in output:\n%s", tt.report, want, out)
				}
			}
		})
	}
}

func TestRenderReportPeriodAndRange(t *testing.T) {
	p := Params{
		Source:     "demo",
		SourceType: "github",
		Since:      "2024-01-01",
		Until:      "2024-12-31",
	}

	out := renderReport(p, "", nil, 0)
	if !strings.Contains(out, "- Период: 2024-01-01 — 2024-12-31") {
		t.Errorf("missing period line in output:\n%s", out)
	}

	p.FromCommit = "abc123"
	out = renderReport(p, "", nil, 0)
	if !strings.Contains(out, "- Диапазон коммитов: abc123..HEAD") {
		t.Errorf("missing commit range line in output:\n%s", out)
	}
}

func TestRenderReportEmptyDecisions(t *testing.T) {
	meta := []struct {
		report string
		empty  string
	}{
		{ReportDecisions, "Решения не найдены."},
		{ReportArchitecture, "Архитектурные изменения не найдены."},
		{ReportTechDebt, "Записи о долге не найдены."},
		{ReportTeam, "Наблюдения не найдены."},
	}

	for _, m := range meta {
		out := renderReport(Params{ReportType: m.report}, "", nil, 0)
		if !strings.Contains(out, m.empty) {
			t.Errorf("renderReport(%s) missing empty label %q", m.report, m.empty)
		}
	}
}
