package engine

import (
	"encoding/json"
	"github.com/zupit-it/zupit-automations/internal/model"
	"strings"
	"testing"
)

func all(c ...model.Condition) model.Group { return model.Group{Mode: "all", Conditions: c} }
func cond(field, op, value string) model.Condition {
	return model.Condition{Field: field, Operator: op, Value: value, IgnoreCase: true}
}
func issue() model.Action {
	return model.Action{Kind: "jira", ConnectionID: 2, Project: "TEST", BoardID: 3, IssueType: "10001", Subject: "{{ .subject }}", Body: "{{ .body }}"}
}
func definition(branches ...model.Branch) model.Definition {
	return model.Definition{Version: 1, Filter: all(), Branches: branches}
}
func branch(name string, g model.Group, a model.Action) model.Branch {
	return model.Branch{Name: name, When: g, Actions: []model.Action{a}}
}

func TestInitialScenariosSynthetic(t *testing.T) {
	srt := definition(branch("SBC escluso", all(cond("subject", "contains", "SBC")), model.Action{Kind: "ignore", Reason: "SBC dismesso"}), branch("SRT", all(), issue()))
	srt.Filter = all(cond("from", "equals", "support@example.test"))
	cup := definition(branch("unità erogante", all(cond("subject", "contains", "unità erogante")), issue()), branch("bug", all(cond("subject", "contains", "segnalazione")), issue()), branch("suggerimento", all(cond("subject", "contains", "suggerimento")), model.Action{Kind: "forward", ConnectionID: 1, To: "po@example.test", Subject: "{{ .subject }}", Body: "{{ .body }}"}))
	cup.Filter = all(cond("from", "equals", "cup@example.test"), cond("subject", "prefix", "nuov"))
	kit := definition(branch("WF", all(cond("reference", "prefix", "WF-")), model.Action{Kind: "slack", ConnectionID: 3, ListID: "FTEST", Fields: []model.Mapping{{Field: "ColTitle", Type: "text", Value: "{{ .reference }}: {{ .subject }}"}}}), branch("altre call", all(), model.Action{Kind: "slack", ConnectionID: 3, ListID: "FOTHER", Fields: []model.Mapping{{Field: "ColTitle", Type: "text", Value: "{{ .subject }}"}}}))
	kit.Filter = all(cond("from", "equals", "help@example.test"))
	kit.Extractors = []model.Extractor{{Name: "reference", Source: "body", Pattern: `(?:CALL|SR):\s*(\S+)`, Required: true}}
	notJira := cond("from", "contains", "jira")
	notJira.Not = true
	support := definition(branch("support", all(), model.Action{Kind: "slack", ConnectionID: 3, ListID: "FSUPPORT", Fields: []model.Mapping{{Field: "ColTitle", Type: "text", Value: "{{ .subject }}"}}}))
	support.Filter = all(notJira)
	for _, tt := range []struct {
		name                            string
		d                               model.Definition
		from, subject, body, kind, list string
	}{
		{"SRT", srt, "support@example.test", "Errore anagrafe", "dettagli", "jira", ""},
		{"SBC escluso", srt, "support@example.test", "[SBC] errore", "", "ignore", ""},
		{"mittente estraneo", srt, "other@example.test", "Errore", "", "", ""},
		{"unità", cup, "cup@example.test", "Nuova richiesta unità erogante", "", "jira", ""},
		{"bug CUP", cup, "cup@example.test", "Nuova segnalazione", "", "jira", ""},
		{"PO", cup, "cup@example.test", "Nuovo suggerimento", "", "forward", ""},
		{"call WF", kit, "help@example.test", "Nuova call", "CALL: WF-42", "slack", "FTEST"},
		{"SR altra", kit, "help@example.test", "Nuova SR", "SR: FND-13", "slack", "FOTHER"},
		{"Support", support, "external@example.test", `Errore "città" e l'apostrofo`, "", "slack", "FSUPPORT"},
		{"Jira escluso", support, "jira@notifications.example.test", "Nuovo ticket", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actions, _, err := Plan(tt.d, model.Message{From: tt.from, Subject: tt.subject, Body: tt.body})
			if err != nil {
				t.Fatal(err)
			}
			if tt.kind == "" {
				if len(actions) != 0 {
					t.Fatal(actions)
				}
				return
			}
			if len(actions) != 1 || actions[0].Kind != tt.kind || actions[0].ListID != tt.list {
				t.Fatalf("unexpected actions: %+v", actions)
			}
			if tt.kind == "jira" && actions[0].Subject != tt.subject {
				t.Fatal("subject altered")
			}
		})
	}
}
func TestMissingFieldsAndInvalidDefinitions(t *testing.T) {
	d := definition(branch("main", all(), issue()))
	d.Extractors = []model.Extractor{{Name: "ticket", Source: "body", Pattern: `ID: (?P<reference>\d+)`, Required: true}}
	if _, _, err := Plan(d, model.Message{Body: "nessun riferimento"}); err == nil {
		t.Fatal("missing required capture accepted")
	}
	d.Branches[0].Actions[0].Subject = "{{ .reference }}"
	actions, _, err := Plan(d, model.Message{Body: "ID: 42"})
	if err != nil || actions[0].Subject != "42" {
		t.Fatalf("capture: %v %+v", err, actions)
	}
	d.Branches[0].Actions[0].Subject = "{{ .missing }}"
	if Validate(d) == nil {
		t.Fatal("undefined template accepted")
	}
	d.Branches[0].Actions[0].Subject = "{{ .subject }}"
	d.Extractors[0].Pattern = "("
	if Validate(d) == nil {
		t.Fatal("bad regex accepted")
	}
}
func TestFirstBranchAndImmutableTemplate(t *testing.T) {
	d := definition(branch("first", all(), issue()), branch("second", all(), issue()))
	d.Branches[0].Actions[0].Fields = []model.Mapping{{Field: "customfield_1", Type: "text", Value: "{{ .subject }}"}}
	before, _ := json.Marshal(d)
	a, which, err := Plan(d, model.Message{Subject: `"à" <script>alert(1)</script>`})
	after, _ := json.Marshal(d)
	if err != nil || which != "first" || len(a) != 1 || string(before) != string(after) {
		t.Fatalf("bad planning: %s %v", which, err)
	}
	if !strings.Contains(a[0].Fields[0].Value, "<script>") {
		t.Fatal("text was destructively sanitized instead of encoded at destination")
	}
}
func TestGroupsAndTemplateFunctions(t *testing.T) {
	g := model.Group{Mode: "any", Conditions: []model.Condition{cond("subject", "regex", "^nuova"), cond("from", "equals", "allowed@example.test")}}
	if !Match(g, map[string]string{"subject": "NUOVA richiesta"}) {
		t.Fatal("case insensitive regex failed")
	}
	d := definition(branch("main", all(), issue()))
	d.Branches[0].Actions[0].Subject = `{{ lower (trim .subject) }} {{ urlquery .from }}`
	a, _, err := Plan(d, model.Message{Subject: " ÈRRore ", From: "a+b@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Subject != "èrrore a%2Bb%40example.test" {
		t.Fatalf("template: %q", a[0].Subject)
	}
}
