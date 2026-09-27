package moderation

import (
	"strings"
	"testing"
)

// A review of eight scores and no words has nothing to read, and must send nothing anywhere.
func TestTextEmptyWhenNothingToRead(t *testing.T) {
	if got := Text(Parts{Notes: map[string]string{"staff_care": "  "}}); got != "" {
		t.Fatalf("Text of a wordless review = %q, want empty", got)
	}
}

// Every readable part is present, labelled, and in a stable order -- the same review must
// produce the same question every time it is asked.
func TestTextCarriesEveryReadablePart(t *testing.T) {
	p := Parts{
		Body:          "genel olarak iyi",
		PurchasedItem: "nevresim takımı",
		Notes:         map[string]string{"staff_care": "ilgilenmediler", "availability": "aradığımı bulamadım"},
	}
	got := Text(p)
	for _, want := range []string{"Note on availability: aradığımı bulamadım", "Note on staff care: ilgilenmediler", "What was bought: nevresim takımı", "Review text: genel olarak iyi"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text missing %q in %q", want, got)
		}
	}
	if strings.Index(got, "availability") > strings.Index(got, "staff care") {
		t.Errorf("notes out of order: %q", got)
	}
	if Text(p) != got {
		t.Error("Text is not deterministic")
	}
}

// Two levels and no middle: any finding at all holds the review.
func TestSevereIsAnyFinding(t *testing.T) {
	if (Verdict{}).Severe() {
		t.Error("an empty verdict is severe")
	}
	if !(Verdict{Findings: []Finding{{Kind: Insult, Quote: "x"}}}).Severe() {
		t.Error("a verdict with a finding is not severe")
	}
}

// A kind the model invented is kept as other_crime, never dropped: dropping it would publish
// exactly what the model was worried about.
func TestCleanKeepsUnknownKinds(t *testing.T) {
	out := Clean(Verdict{Findings: []Finding{{Kind: "defamation", Quote: " hırsız "}, {Kind: Threat, Quote: "bulacağım"}}})
	if len(out.Findings) != 2 {
		t.Fatalf("Clean dropped findings: %+v", out.Findings)
	}
	if out.Findings[0].Kind != OtherCrime || out.Findings[0].Quote != "hırsız" {
		t.Errorf("unknown kind not kept as other_crime: %+v", out.Findings[0])
	}
	if out.Findings[1].Kind != Threat {
		t.Errorf("known kind changed: %+v", out.Findings[1])
	}
}

// The prompt must name what not to report as clearly as what to report. A check that holds
// back honest complaints is a check that stops people writing them.
func TestPromptSaysWhatNotToReport(t *testing.T) {
	p := Prompt("x")
	for _, want := range []string{"personel ilgisizdi", "fiyatlar çok pahalı", "Do not report", "When you are unsure"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
