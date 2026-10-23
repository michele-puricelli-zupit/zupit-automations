package web

import (
	"errors"
	"github.com/zupit-it/zupit-automations/internal/model"
	"net/url"
	"strconv"
	"strings"
)

func intValue(s string) int { n, _ := strconv.Atoi(s); return n }
func groupForm(v url.Values, prefix string) model.Group {
	g := model.Group{Mode: v.Get(prefix + ".mode")}
	if g.Mode == "" {
		g.Mode = "all"
	}
	for i := 0; i < 100; i++ {
		p := prefix + "." + strconv.Itoa(i) + "."
		if !v.Has(p + "field") {
			break
		}
		g.Conditions = append(g.Conditions, model.Condition{Field: v.Get(p + "field"), Operator: v.Get(p + "operator"), Value: v.Get(p + "value"), Not: v.Has(p + "not"), IgnoreCase: v.Has(p + "ignore_case")})
	}
	return g
}
func parseFlow(v url.Values) model.Flow {
	f := model.Flow{ID: intValue(v.Get("id")), Name: strings.TrimSpace(v.Get("name")), SourceID: intValue(v.Get("source_id")), Enabled: v.Has("enabled"), Definition: model.Definition{Version: 1, Filter: groupForm(v, "filter")}}
	if v.Has("comparison") {
		f.Definition.Comparison = &model.ComparisonSettings{Instructions: strings.TrimSpace(v.Get("comparison.instructions")), SlackConnectionID: intValue(v.Get("comparison.slack_connection_id")), SlackRecipient: strings.TrimSpace(v.Get("comparison.slack_recipient"))}
	}
	for i := 0; i < 50; i++ {
		p := "extract." + strconv.Itoa(i) + "."
		if !v.Has(p + "name") {
			break
		}
		f.Definition.Extractors = append(f.Definition.Extractors, model.Extractor{Name: v.Get(p + "name"), Source: v.Get(p + "source"), Pattern: v.Get(p + "pattern"), Required: v.Has(p + "required")})
	}
	for bi := 0; bi < 50; bi++ {
		p := "branch." + strconv.Itoa(bi) + "."
		if !v.Has(p + "name") {
			break
		}
		b := model.Branch{Name: v.Get(p + "name"), When: groupForm(v, p+"when")}
		for ai := 0; ai < 20; ai++ {
			ap := p + "action." + strconv.Itoa(ai) + "."
			if !v.Has(ap + "kind") {
				break
			}
			a := model.Action{Kind: v.Get(ap + "kind"), ConnectionID: intValue(v.Get(ap + "connection_id")), Project: v.Get(ap + "project"), BoardID: intValue(v.Get(ap + "board_id")), IssueType: v.Get(ap + "issue_type"), Reporter: v.Get(ap + "reporter"), ServiceDeskID: v.Get(ap + "service_desk_id"), RequestTypeID: v.Get(ap + "request_type_id"), ListID: v.Get(ap + "list_id"), To: v.Get(ap + "to"), Subject: v.Get(ap + "subject"), Body: v.Get(ap + "body"), Reason: v.Get(ap + "reason")}
			if value := v.Get(ap + "queue_id"); value != "" {
				queueID, err := strconv.Atoi(value)
				if err != nil {
					queueID = -1
				}
				a.QueueID = queueID
			}
			for fi := 0; fi < 100; fi++ {
				fp := ap + "field." + strconv.Itoa(fi) + "."
				if !v.Has(fp + "field") {
					break
				}
				a.Fields = append(a.Fields, model.Mapping{Field: v.Get(fp + "field"), Type: v.Get(fp + "type"), Value: v.Get(fp + "value")})
			}
			b.Actions = append(b.Actions, a)
		}
		f.Definition.Branches = append(f.Definition.Branches, b)
	}
	return f
}
func editorOperation(f *model.Flow, operation string) error {
	if operation == "" {
		return nil
	}
	p := strings.Split(operation, ".")
	n := func(i int) int {
		if i >= len(p) {
			return -1
		}
		v, err := strconv.Atoi(p[i])
		if err != nil {
			return -1
		}
		return v
	}
	condition := func() model.Condition {
		return model.Condition{Field: "subject", Operator: "contains", IgnoreCase: true}
	}
	branch := func(i int) *model.Branch {
		if i < 0 || i >= len(f.Definition.Branches) {
			return nil
		}
		return &f.Definition.Branches[i]
	}
	action := func(bi, ai int) *model.Action {
		b := branch(bi)
		if b == nil || ai < 0 || ai >= len(b.Actions) {
			return nil
		}
		return &b.Actions[ai]
	}
	invalid := errors.New("operazione editor non valida o limite raggiunto")
	switch p[0] {
	case "filter":
		if len(f.Definition.Filter.Conditions) >= 100 {
			return invalid
		}
		f.Definition.Filter.Conditions = append(f.Definition.Filter.Conditions, condition())
		return nil
	case "extract":
		if len(f.Definition.Extractors) >= 50 {
			return invalid
		}
		f.Definition.Extractors = append(f.Definition.Extractors, model.Extractor{Source: "body"})
		return nil
	case "branch":
		if len(f.Definition.Branches) >= 50 {
			return invalid
		}
		f.Definition.Branches = append(f.Definition.Branches, model.Branch{Name: "Nuovo ramo", When: model.Group{Mode: "all"}, Actions: []model.Action{{Kind: "ignore", Reason: "Da configurare"}}})
		return nil
	case "condition":
		if b := branch(n(1)); b != nil && len(b.When.Conditions) < 100 {
			b.When.Conditions = append(b.When.Conditions, condition())
			return nil
		}
	case "action":
		if b := branch(n(1)); b != nil && len(b.Actions) < 20 {
			b.Actions = append(b.Actions, model.Action{Kind: "ignore", Reason: "Da configurare"})
			return nil
		}
	case "field":
		if a := action(n(1), n(2)); a != nil && len(a.Fields) < 100 {
			a.Fields = append(a.Fields, model.Mapping{Type: "text"})
			return nil
		}
	case "remove":
		if len(p) < 3 {
			return invalid
		}
		switch p[1] {
		case "extract":
			i := n(2)
			if i >= 0 && i < len(f.Definition.Extractors) {
				f.Definition.Extractors = append(f.Definition.Extractors[:i], f.Definition.Extractors[i+1:]...)
				return nil
			}
		case "branch":
			i := n(2)
			if i >= 0 && i < len(f.Definition.Branches) {
				f.Definition.Branches = append(f.Definition.Branches[:i], f.Definition.Branches[i+1:]...)
				return nil
			}
		case "action":
			if b := branch(n(2)); b != nil {
				i := n(3)
				if i >= 0 && i < len(b.Actions) {
					b.Actions = append(b.Actions[:i], b.Actions[i+1:]...)
					return nil
				}
			}
		case "field":
			if a := action(n(2), n(3)); a != nil {
				i := n(4)
				if i >= 0 && i < len(a.Fields) {
					a.Fields = append(a.Fields[:i], a.Fields[i+1:]...)
					return nil
				}
			}
		case "condition":
			var g *model.Group
			if p[2] == "filter" {
				g = &f.Definition.Filter
			} else if p[2] == "branch" {
				if b := branch(n(3)); b != nil {
					g = &b.When
				}
			}
			if g != nil {
				i := n(len(p) - 1)
				if i >= 0 && i < len(g.Conditions) {
					g.Conditions = append(g.Conditions[:i], g.Conditions[i+1:]...)
					return nil
				}
			}
		}
	}
	return invalid
}
