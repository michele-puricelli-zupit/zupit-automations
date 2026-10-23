package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/views"
)

func (s *Server) jiraChoices(r *http.Request, d *views.Data) {
	d.Jira = make(map[string]connectors.JiraChoices)
	cache := make(map[string]connectors.JiraChoices)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	for bi, b := range d.Flow.Definition.Branches {
		for ai, a := range b.Actions {
			if a.Kind != "jira" && a.Kind != "jira_request" {
				continue
			}
			choices := connectors.JiraChoices{Error: "Scegli una connessione Jira per caricare le destinazioni."}
			if s.Clients != nil {
				for _, conn := range d.Connections {
					if conn.ID != a.ConnectionID || conn.Provider != "jira" {
						continue
					}
					cacheKey := fmt.Sprintf("%d/%s/%s/%s", conn.ID, a.Kind, a.Project, a.ServiceDeskID)
					var ok bool
					choices, ok = cache[cacheKey]
					if !ok {
						choices = s.Clients.JiraDestinations(ctx, conn, a)
						cache[cacheKey] = choices
					}
					break
				}
			}
			d.Jira[fmt.Sprintf("%d.%d", bi, ai)] = choices
		}
	}
}
