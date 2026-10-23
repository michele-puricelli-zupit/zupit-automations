package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zupit-it/zupit-automations/internal/model"
	"strings"
)

func (c *Clients) slack(ctx context.Context, id int, method string, body, out any) error {
	token, err := c.token(ctx, id)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err = c.do(ctx, c.HTTP, "POST", "https://slack.com/api/"+method, body, map[string]string{"Authorization": "Bearer " + token}, &raw); err != nil {
		return err
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return &Failure{Message: "Risposta Slack non valida", Uncertain: true}
	}
	if !response.OK {
		return slackFailure(method, response.Error)
	}
	return json.Unmarshal(raw, out)
}

func slackFailure(method, code string) *Failure {
	f := &Failure{Message: "Richiesta Slack rifiutata", HTTPStatus: 200, SlackMethod: method, SlackCode: code}
	switch code {
	case "invalid_auth", "not_authed", "token_revoked", "account_inactive":
		f.Auth = true
		f.Message = "Token Slack revocato o non valido"
	case "ratelimited", "rate_limited":
		f.Retryable = true
		f.Message = "Limite richieste Slack raggiunto"
	case "internal_error", "fatal_error", "request_timeout":
		f.Uncertain = true
		f.Message = "Slack non ha confermato l’esito della richiesta"
	case "missing_scope", "access_denied", "no_permission":
		f.Message = "Permessi Slack insufficienti"
	case "invalid_list_id", "file_not_found", "invalid_column_id", "invalid_initial_fields":
		f.Message = "Lista o colonne Slack non valide"
	case "messages_tab_disabled":
		f.Message = "Abilitare la scheda Messaggi nell’App Home della Slack App"
	case "channel_not_found", "user_not_found", "users_not_found":
		f.Message = "Destinatario Slack non trovato: verificare ID e workspace della connessione"
	case "cannot_dm_bot", "cannot_dm_self", "user_disabled":
		f.Message = "Slack non consente un DM a questo destinatario"
	case "not_in_channel", "is_archived", "restricted_action", "app_access_restricted":
		f.Message = "Accesso alla conversazione Slack non consentito"
	case "invalid_arguments", "invalid_blocks", "invalid_blocks_format", "msg_blocks_too_long", "no_text", "not_allowed_token_type":
	default:
		// Log only known API codes; unexpected response text may contain private data.
		f.SlackCode = "unrecognized_error"
	}
	f.Message += fmt.Sprintf(" (%s: %s)", f.SlackMethod, f.SlackCode)
	return f
}

func slackFields(fields []model.Mapping) ([]any, error) {
	initial := make([]any, 0, len(fields))
	for _, f := range fields {
		v := map[string]any{"column_id": f.Field}
		switch f.Type {
		case "text":
			v["rich_text"] = []any{map[string]any{"type": "rich_text", "elements": []any{map[string]any{"type": "rich_text_section", "elements": []any{map[string]any{"type": "text", "text": f.Value}}}}}}
		case "number":
			var number json.Number = json.Number(f.Value)
			if _, err := number.Float64(); err != nil {
				return nil, errors.New("numero Slack non valido")
			}
			v["number"] = []any{number}
		case "boolean":
			if f.Value != "true" && f.Value != "false" {
				return nil, errors.New("checkbox Slack non valida")
			}
			v["checkbox"] = f.Value == "true"
		case "option":
			v["select"] = []string{f.Value}
		case "user":
			v["user"] = []string{f.Value}
		case "date":
			v["date"] = []string{f.Value}
		case "link":
			v["link"] = []any{map[string]string{"original_url": f.Value}}
		case "json":
			var value map[string]any
			if err := json.Unmarshal([]byte(f.Value), &value); err != nil {
				return nil, errors.New("campo Slack JSON non valido")
			}
			for k, item := range value {
				if k == "column_id" {
					return nil, errors.New("column_id va specificato nel campo dedicato")
				}
				v[k] = item
			}
		default:
			return nil, fmt.Errorf("tipo Slack non supportato: %s", f.Type)
		}
		initial = append(initial, v)
	}
	return initial, nil
}
func (c *Clients) createListItem(ctx context.Context, conn model.Connection, a model.Action) (model.Result, error) {
	fields, err := slackFields(a.Fields)
	if err != nil {
		return model.Result{}, err
	}
	var out struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	if err = c.slack(ctx, conn.ID, "slackLists.items.create", map[string]any{"list_id": a.ListID, "initial_fields": fields}, &out); err != nil {
		return model.Result{}, err
	}
	if out.Item.ID == "" {
		return model.Result{}, &Failure{Message: "Slack non ha restituito il riferimento dell’elemento", Uncertain: true}
	}
	return model.Result{Reference: "slack://list/" + a.ListID + "/" + out.Item.ID}, nil
}
func (c *Clients) Alert(ctx context.Context, conn model.Connection, body string) error {
	var out map[string]any
	return c.slack(ctx, conn.ID, "chat.postMessage", map[string]any{"channel": conn.Config["alert_channel"], "text": body, "unfurl_links": false, "unfurl_media": false}, &out)
}

func (c *Clients) ComparisonDM(ctx context.Context, connectionID int, recipient string) (string, error) {
	if !model.ValidSlackRecipient(recipient) {
		return "", &Failure{Message: "Destinatario del confronto: usare un ID membro o DM personale, non un canale"}
	}
	conn, err := c.Store.Connection(ctx, connectionID)
	if err != nil || conn.Provider != "slack" || conn.Status != "ready" {
		return "", &Failure{Message: "Connessione Slack del confronto non pronta"}
	}
	body := map[string]any{"return_im": true}
	if strings.HasPrefix(recipient, "D") {
		body["channel"] = recipient
	} else {
		body["users"] = recipient
	}
	var out struct {
		Channel struct {
			ID   string `json:"id"`
			IsIM bool   `json:"is_im"`
		} `json:"channel"`
	}
	if err = c.slack(ctx, connectionID, "conversations.open", body, &out); err != nil {
		f := Classify(err)
		if f.Uncertain {
			f.Uncertain, f.Retryable = false, true
		}
		return "", f
	}
	if !out.Channel.IsIM || !strings.HasPrefix(out.Channel.ID, "D") || !model.ValidSlackRecipient(out.Channel.ID) {
		return "", &Failure{Message: "Slack non ha restituito un DM individuale"}
	}
	return out.Channel.ID, nil
}

func (c *Clients) SendComparison(ctx context.Context, cfg model.ComparisonSettings, text string) (string, error) {
	channel, err := c.ComparisonDM(ctx, cfg.SlackConnectionID, cfg.SlackRecipient)
	if err != nil {
		return "", err
	}
	if len([]rune(text)) > 3000 {
		return "", &Failure{Message: "Report Slack troppo lungo"}
	}
	fallback := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
	var out struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	err = c.slack(ctx, cfg.SlackConnectionID, "chat.postMessage", map[string]any{
		"channel": channel, "text": fallback, "parse": "none", "unfurl_links": false, "unfurl_media": false,
		"blocks": []any{map[string]any{"type": "section", "text": map[string]any{"type": "plain_text", "text": text, "emoji": false}}},
	}, &out)
	if err != nil {
		return "", err
	}
	if out.Channel != channel || out.TS == "" {
		return "", &Failure{Message: "Slack non ha confermato l'invio del report", Uncertain: true}
	}
	return out.Channel + ":" + out.TS, nil
}
