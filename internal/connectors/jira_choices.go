package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/zupit-it/zupit-automations/internal/model"
)

type JiraOption struct {
	Value, Label string
}

type JiraChoices struct {
	Projects, Boards, IssueTypes, ServiceDesks, RequestTypes, Queues []JiraOption
	Error, QueueError                                                string
}

type jiraEntry struct {
	ID          json.Number `json:"id"`
	Name        string      `json:"name"`
	Key         string      `json:"key"`
	ProjectName string      `json:"projectName"`
	ProjectKey  string      `json:"projectKey"`
	Subtask     bool        `json:"subtask"`
}

// Jira uses two pagination formats; always build the next URL locally to keep credentials on the configured host.
func (c *Clients) jiraOptions(ctx context.Context, conn model.Connection, path string) ([]JiraOption, error) {
	var options []JiraOption
	for start := 0; ; {
		var page struct {
			Values     []jiraEntry `json:"values"`
			IssueTypes []jiraEntry `json:"issueTypes"`
			Total      int         `json:"total"`
			IsLast     bool        `json:"isLast"`
			IsLastPage bool        `json:"isLastPage"`
		}
		u, err := url.Parse(path)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		if strings.HasPrefix(path, "/rest/servicedeskapi/") {
			q.Set("start", fmt.Sprint(start))
			q.Set("limit", "100")
		} else {
			q.Set("startAt", fmt.Sprint(start))
			q.Set("maxResults", "100")
		}
		u.RawQuery = q.Encode()
		if err := c.jira(ctx, conn, "GET", u.String(), nil, &page); err != nil {
			return nil, err
		}
		entries := page.Values
		if page.IssueTypes != nil {
			entries = page.IssueTypes
		}
		for _, entry := range entries {
			if entry.Subtask {
				continue
			}
			value, label := entry.ID.String(), entry.Name
			if entry.Key != "" {
				value, label = entry.Key, entry.Name+" · "+entry.Key
			}
			if entry.ProjectKey != "" {
				label = entry.ProjectName + " · " + entry.ProjectKey
			}
			options = append(options, JiraOption{Value: value, Label: label})
		}
		start += len(entries)
		if len(entries) == 0 || page.IsLast || page.IsLastPage || (page.Total > 0 && start >= page.Total) {
			break
		}
	}
	sort.SliceStable(options, func(i, j int) bool { return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label) })
	return options, nil
}

func (c *Clients) JiraDestinations(ctx context.Context, conn model.Connection, a model.Action) JiraChoices {
	var result JiraChoices
	if conn.Provider != "jira" {
		result.Error = "Scegli una connessione Jira per caricare le destinazioni."
		return result
	}
	load := func(target *[]JiraOption, path, label string) {
		options, err := c.jiraOptions(ctx, conn, path)
		if err != nil {
			if result.Error != "" {
				result.Error += " "
			}
			result.Error += label + ": " + Classify(err).Message + ". Verifica la connessione e i permessi Jira."
			return
		}
		*target = options
	}
	if a.Kind == "jira" {
		load(&result.Projects, "/rest/api/3/project/search", "Progetti non disponibili")
		if a.Project != "" {
			load(&result.Boards, "/rest/agile/1.0/board?projectKeyOrId="+url.QueryEscape(a.Project), "Board non disponibili")
			load(&result.IssueTypes, "/rest/api/3/issue/createmeta/"+url.PathEscape(a.Project)+"/issuetypes", "Tipi di ticket non disponibili")
		}
	} else {
		load(&result.ServiceDesks, "/rest/servicedeskapi/servicedesk", "Service Desk non disponibili")
		if a.ServiceDeskID != "" {
			base := "/rest/servicedeskapi/servicedesk/" + url.PathEscape(a.ServiceDeskID)
			load(&result.RequestTypes, base+"/requesttype", "Tipi di richiesta non disponibili")
			var err error
			result.Queues, err = c.jiraOptions(ctx, conn, base+"/queue")
			if err != nil {
				result.QueueError = "Code non disponibili: " + Classify(err).Message + ". Per accedere alle code servono permessi da agente. Puoi creare richieste senza scegliere una coda."
			}
		}
	}
	return result
}
