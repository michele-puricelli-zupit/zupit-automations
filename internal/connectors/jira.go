package connectors

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/engine"
	"github.com/zupit-it/zupit-automations/internal/model"
	"net/url"
	"strings"
)

func (c *Clients) jira(ctx context.Context, conn model.Connection, method, path string, body, out any) error {
	token, err := c.token(ctx, conn.ID)
	if err != nil {
		return err
	}
	return c.do(ctx, c.HTTP, method, strings.TrimRight(conn.Config["url"], "/")+path, body, map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(conn.Config["email"]+":"+token))}, out)
}
func document(value string) map[string]any {
	paragraphs := []any{}
	for _, line := range strings.Split(value, "\n") {
		p := map[string]any{"type": "paragraph", "content": []any{}}
		if line != "" {
			p["content"] = []any{map[string]any{"type": "text", "text": line}}
		}
		paragraphs = append(paragraphs, p)
	}
	return map[string]any{"type": "doc", "version": 1, "content": paragraphs}
}
func (c *Clients) createIssue(ctx context.Context, conn model.Connection, a model.Action) (model.Result, error) {
	fields := map[string]any{}
	for _, f := range a.Fields {
		v, err := engine.Value(f)
		if err != nil {
			return model.Result{}, err
		}
		fields[f.Field] = v
	}
	fields["summary"] = a.Subject
	var out struct {
		Key      string `json:"key"`
		IssueKey string `json:"issueKey"`
	}
	var err error
	if a.Kind == "jira_request" {
		fields["description"] = a.Body
		body := map[string]any{"serviceDeskId": a.ServiceDeskID, "requestTypeId": a.RequestTypeID, "requestFieldValues": fields}
		if a.Reporter != "" {
			body["raiseOnBehalfOf"] = a.Reporter
		}
		err = c.jira(ctx, conn, "POST", "/rest/servicedeskapi/request", body, &out)
	} else {
		fields["project"] = map[string]string{"key": a.Project}
		fields["issuetype"] = map[string]string{"id": a.IssueType}
		fields["description"] = document(a.Body)
		if a.Reporter != "" {
			fields["reporter"] = map[string]string{"accountId": a.Reporter}
		}
		err = c.jira(ctx, conn, "POST", "/rest/api/3/issue", map[string]any{"fields": fields}, &out)
	}
	if err != nil {
		return model.Result{}, err
	}
	key := out.Key
	if key == "" {
		key = out.IssueKey
	}
	if key == "" {
		return model.Result{}, &Failure{Message: "Jira non ha restituito il riferimento del ticket", Uncertain: true}
	}
	result := model.Result{Reference: conn.Config["url"] + "/browse/" + url.PathEscape(key)}
	// Board verification is read-only: a failure must never recreate the issue.
	var board struct {
		Issues []struct {
			Key string `json:"key"`
		} `json:"issues"`
	}
	path := fmt.Sprintf("/rest/agile/1.0/board/%d/issue?jql=%s&maxResults=1", a.BoardID, url.QueryEscape(`key = "`+key+`"`))
	if err = c.jira(ctx, conn, "GET", path, nil, &board); err != nil {
		result.Warning = "Ticket creato; verifica board non riuscita. Controllare permessi, filtro e automazioni Jira."
	} else if len(board.Issues) != 1 || board.Issues[0].Key != key {
		result.Warning = "Ticket creato ma non ancora presente nella board attesa. Verificare filtro e automazioni Jira."
	}
	return result, nil
}
func (c *Clients) VerifyAction(ctx context.Context, a model.Action) error {
	conn, err := c.Store.Connection(ctx, a.ConnectionID)
	if err != nil {
		return err
	}
	if a.Kind == "jira" || a.Kind == "jira_request" {
		var board map[string]any
		if err = c.jira(ctx, conn, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/configuration", a.BoardID), nil, &board); err != nil {
			return err
		}
		var fields map[string]any
		if a.Kind == "jira_request" {
			return c.jira(ctx, conn, "GET", "/rest/servicedeskapi/servicedesk/"+url.PathEscape(a.ServiceDeskID)+"/requesttype/"+url.PathEscape(a.RequestTypeID)+"/field", nil, &fields)
		}
		return c.jira(ctx, conn, "GET", "/rest/api/3/issue/createmeta/"+url.PathEscape(a.Project)+"/issuetypes/"+url.PathEscape(a.IssueType), nil, &fields)
	}
	if a.Kind == "slack" {
		var out map[string]any
		return c.slack(ctx, conn.ID, "slackLists.items.list", map[string]any{"list_id": a.ListID, "limit": 1}, &out)
	}
	return nil
}
