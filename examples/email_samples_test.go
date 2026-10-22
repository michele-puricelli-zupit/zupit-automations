package examples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/engine"
	"github.com/zupit-it/zupit-automations/internal/model"
)

func sampleMessage(t *testing.T, name string) model.Message {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".eml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := connectors.ParseEmail(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func configuredSamplePreset(t *testing.T, name string) model.Definition {
	t.Helper()
	f, err := Preset(name)
	if err != nil {
		t.Fatal(err)
	}
	d := f.Definition
	d.Filter.Conditions[0].Value = "fixture@example.test"
	for bi := range d.Branches {
		for ai := range d.Branches[bi].Actions {
			a := &d.Branches[bi].Actions[ai]
			a.ConnectionID, a.BoardID, a.IssueType = 1, 1, "10001"
			a.Reporter, a.ListID, a.To = "test-reporter", "FTEST", "po@example.test"
			if a.Project == "DA_CONFIGURARE" {
				a.Project = "TEST"
			}
			for fi := range a.Fields {
				a.Fields[fi].Field = "ColTitle"
			}
		}
	}
	return d
}

func TestSRTAssignmentSample(t *testing.T) {
	d := configuredSamplePreset(t, "srt")
	m := sampleMessage(t, "srt-assignment")
	want := map[string]string{
		"task_id": "TAS000000979470", "incident_id": "INC000002356016", "priority": "Low",
		"support_group": "AS - SRT", "summary": "C.APPAG.SRSTR - File allegato domanda di anticipo risulta non firmato in PITre - domanda 29581",
	}
	for variable := range want {
		d.Branches[1].Actions[0].Fields = append(d.Branches[1].Actions[0].Fields, model.Mapping{Field: "customfield_" + variable, Type: "text", Value: "{{ ." + variable + " }}"})
	}
	for _, body := range []string{m.Body, strings.ReplaceAll(m.Body, "\n", "\r\n")} {
		m.Body = body
		actions, branch, err := engine.Plan(d, m)
		if err != nil || branch != "Segnalazione SRT" || len(actions) != 1 {
			t.Fatalf("SRT sample: actions=%+v branch=%q error=%v", actions, branch, err)
		}
		a := actions[0]
		if a.Kind != "jira" || a.Subject != m.Subject || a.Body != m.Body {
			t.Fatal("SRT destination must preserve the original subject and message")
		}
		for _, field := range a.Fields {
			variable := strings.TrimPrefix(field.Field, "customfield_")
			if field.Value != want[variable] {
				t.Errorf("%s=%q, want %q", variable, field.Value, want[variable])
			}
		}
	}
	m.Body = strings.ReplaceAll(m.Body, "AS - SRT", "AS - OTHER")
	if actions, _, err := engine.Plan(d, m); err != nil || len(actions) != 0 {
		t.Fatalf("assignment to another group was accepted: %+v %v", actions, err)
	}
	m = sampleMessage(t, "srt-assignment")
	m.Subject = "[SBC] " + m.Subject
	if actions, branch, err := engine.Plan(d, m); err != nil || branch != "SBC escluso" || len(actions) != 1 || actions[0].Kind != "ignore" {
		t.Fatalf("SBC exclusion lost: %+v %q %v", actions, branch, err)
	}
	m = sampleMessage(t, "srt-assignment")
	m.Body = strings.ReplaceAll(m.Body, "TAS000000979470", "task non disponibile")
	if actions, _, err := engine.Plan(d, m); err == nil || len(actions) != 0 {
		t.Fatal("missing task identifier must stop creation")
	}
}

func TestKey2helpAssignmentSample(t *testing.T) {
	d := configuredSamplePreset(t, "kit")
	// Project routing and destination IDs remain synthetic until the real mapping is supplied.
	d.Branches[0].When.Conditions[0].Value = "TEST-WORKFLOW"
	d.Branches[1].When.Conditions[0].Value = "TEST-ORBEON"
	want := map[string]string{
		"reference": "SR-123827", "project": "PGD - Configurazioni Digit_R35_R7",
		"summary": "verifica oggetto protocolli in errore", "service": "E-Service [PGD - SAP CRM]",
		"support_group": "SIAG - PAB Foundation", "ticket_url": "",
	}
	for variable := range want {
		d.Branches[2].Actions[0].Fields = append(d.Branches[2].Actions[0].Fields, model.Mapping{Field: variable, Type: "text", Value: "{{ ." + variable + " }}"})
	}
	m := sampleMessage(t, "key2help-assignment")
	actions, branch, err := engine.Plan(d, m)
	if err != nil || branch != "Call Foundation" || len(actions) != 1 || actions[0].Kind != "slack" {
		t.Fatalf("key2help sample: %+v %q %v", actions, branch, err)
	}
	if actions[0].Fields[0].Value != "SR-123827 · verifica oggetto protocolli in errore" {
		t.Fatalf("unhelpful Slack title: %q", actions[0].Fields[0].Value)
	}
	for _, field := range actions[0].Fields[1:] {
		if field.Value != want[field.Field] {
			t.Errorf("%s=%q, want %q", field.Field, field.Value, want[field.Field])
		}
	}
	// The same SR identifier must follow the configured project, not the SR prefix.
	for _, tt := range []struct{ project, branch string }{{"TEST-WORKFLOW", "Workflow"}, {"TEST-ORBEON", "Orbeon"}} {
		changed := m
		changed.Body = strings.ReplaceAll(m.Body, "PGD - Configurazioni Digit_R35_R7", tt.project+" - Esempio")
		if actions, branch, err := engine.Plan(d, changed); err != nil || branch != tt.branch || len(actions) != 1 {
			t.Fatalf("project routing ignored: %+v %q %v", actions, branch, err)
		}
	}
	m.Body = strings.ReplaceAll(m.Body, "“PGD - Configurazioni Digit_R35_R7”", "progetto non disponibile")
	if actions, _, err := engine.Plan(d, m); err == nil || len(actions) != 0 {
		t.Fatal("missing project must stop creation instead of using the fallback")
	}
}

func TestKey2helpTicketLinkFromHTMLButton(t *testing.T) {
	d := configuredSamplePreset(t, "kit")
	d.Branches[2].Actions[0].Fields = append(d.Branches[2].Actions[0].Fields, model.Mapping{Field: "ColTicket", Type: "link", Value: "{{ .ticket_url }}"})
	raw := "From: fixture@example.test\r\nTo: team@example.test\r\nSubject: Esempio HTML sintetico\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" +
		`<p>Il Task (verifica oggetto protocolli in errore)<br>"PGD - Configurazioni Digit_R35_R7"</p>` +
		`<p><a href="https://tickets.example.test/SR-123827?view=detail&amp;source=email">Accedi al ticket</a></p>` +
		`<p>Ticket in ky2help: SR-123827</p>` +
		`<p>I file di configurazione sono in <a href="https://documents.example.test/configurazione">SharePoint</a></p>`
	m, err := connectors.ParseEmail(raw)
	if err != nil {
		t.Fatal(err)
	}
	actions, _, err := engine.Plan(d, m)
	if err != nil || len(actions) != 1 {
		t.Fatalf("HTML assignment failed: %+v %v", actions, err)
	}
	if got := actions[0].Fields[1].Value; got != "https://tickets.example.test/SR-123827?view=detail&source=email" {
		t.Fatalf("ticket button URL confused with another link: %q", got)
	}
}

func TestCUPJiraResultIsNotANewRequest(t *testing.T) {
	d := configuredSamplePreset(t, "cup")
	for i := range d.Filter.Conditions {
		if d.Filter.Conditions[i].Field == "subject" {
			d.Filter.Conditions[i].Value = ".*"
		}
	}
	d.Branches[1].When.Conditions[0].Value = "Segnalazione bug"
	m := sampleMessage(t, "cup-jira-result")
	if actions, _, err := engine.Plan(d, m); err != nil || len(actions) != 0 {
		t.Fatalf("Jira result would create a duplicate: %+v %v", actions, err)
	}
	// Synthetic input checks the known output mapping; the original CUP source email is still missing.
	m.Body = "Nuova segnalazione problema software POST\nDescrizione dettagliata Errore con la visita \"SPORTIVA\"\nScreenshot / allegati https://drive.google.com/open?id=fixture-1"
	actions, branch, err := engine.Plan(d, m)
	if err != nil || branch != "Segnalazione bug" || len(actions) != 1 {
		t.Fatalf("synthetic CUP request: %+v %q %v", actions, branch, err)
	}
	if actions[0].Project != "APPSD" || actions[0].Body != m.Body {
		t.Fatal("CUP bug must preserve the description and target the observed APPSD project")
	}
}
