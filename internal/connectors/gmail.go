package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zupit-it/zupit-automations/internal/model"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/oauth2"
)

const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"

type persistedToken struct {
	mu      sync.Mutex
	source  oauth2.TokenSource
	current *oauth2.Token
	clients *Clients
	id      int
}

func (p *persistedToken) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.source.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response.StatusCode == 400 {
			return nil, &Failure{Message: "Autorizzazione Gmail scaduta o revocata", Auth: true}
		}
		return nil, &Failure{Message: "Rinnovo credenziali Gmail non disponibile", Retryable: true}
	}
	if p.current == nil || p.current.AccessToken != t.AccessToken {
		b, _ := json.Marshal(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = p.clients.Store.SaveSecret(ctx, p.id, b); err != nil {
			return nil, &Failure{Message: "Impossibile conservare le credenziali Gmail rinnovate"}
		}
		p.current = t
	}
	return t, nil
}
func (c *Clients) gmailClient(ctx context.Context, id int) (*http.Client, error) {
	b, err := c.Store.Secret(ctx, id)
	if err != nil {
		return nil, &Failure{Message: "Casella Gmail da ricollegare", Auth: true}
	}
	var token oauth2.Token
	if err = json.Unmarshal(b, &token); err != nil {
		return nil, &Failure{Message: "Credenziali Gmail non valide", Auth: true}
	}
	// Refresh survives the lifetime of the incoming HTTP callback or worker tick.
	refreshCtx := context.WithValue(context.Background(), oauth2.HTTPClient, c.HTTP)
	source := &persistedToken{source: c.GmailOAuth.TokenSource(refreshCtx, &token), current: &token, clients: c, id: id}
	client := oauth2.NewClient(context.Background(), oauth2.ReuseTokenSource(&token, source))
	client.Timeout = 30 * time.Second
	client.CheckRedirect = c.HTTP.CheckRedirect
	if transport, ok := client.Transport.(*oauth2.Transport); ok {
		transport.Base = c.HTTP.Transport
	}
	return client, nil
}
func (c *Clients) ConnectGmail(ctx context.Context, id int, t *oauth2.Token) error {
	b, _ := json.Marshal(t)
	if err := c.Store.SaveSecret(ctx, id, b); err != nil {
		return err
	}
	conn, err := c.Store.Connection(ctx, id)
	if err != nil {
		return err
	}
	return c.Verify(ctx, conn)
}
func (c *Clients) Renew(ctx context.Context, conn model.Connection) error {
	client, err := c.gmailClient(ctx, conn.ID)
	if err != nil {
		return err
	}
	var out struct {
		HistoryID  string `json:"historyId"`
		Expiration string `json:"expiration"`
	}
	if err = c.do(ctx, client, "POST", gmailBase+"/watch", map[string]any{"topicName": conn.Config["topic"], "labelIds": []string{"INBOX"}, "labelFilterBehavior": "INCLUDE"}, nil, &out); err != nil {
		return err
	}
	millis, err := strconv.ParseInt(out.Expiration, 10, 64)
	if err != nil || out.HistoryID == "" {
		return errors.New("iscrizione Gmail non valida")
	}
	return c.Store.Watch(ctx, conn.ID, out.HistoryID, time.UnixMilli(millis), conn.HistoryID == "")
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type gmailPart struct {
	MimeType string        `json:"mimeType"`
	Filename string        `json:"filename"`
	Headers  []gmailHeader `json:"headers"`
	Body     struct {
		Data string `json:"data"`
		Size int    `json:"size"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}
type gmailMessage struct {
	ID           string    `json:"id"`
	InternalDate string    `json:"internalDate"`
	LabelIDs     []string  `json:"labelIds"`
	Payload      gmailPart `json:"payload"`
}

func (c *Clients) message(ctx context.Context, client *http.Client, id string) (model.Message, bool, error) {
	var v gmailMessage
	if err := c.do(ctx, client, "GET", gmailBase+"/messages/"+url.PathEscape(id)+"?format=full", nil, nil, &v); err != nil {
		return model.Message{}, false, err
	}
	for _, label := range v.LabelIDs {
		if label == "SENT" || label == "DRAFT" || label == "SPAM" {
			return model.Message{}, false, nil
		}
	}
	millis, err := strconv.ParseInt(v.InternalDate, 10, 64)
	if err != nil {
		return model.Message{}, false, errors.New("data Gmail non valida")
	}
	m := model.Message{ID: v.ID, ReceivedAt: time.UnixMilli(millis)}
	for _, h := range v.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "from":
			m.From = address(h.Value)
		case "to", "cc":
			if m.To != "" {
				m.To += ", "
			}
			m.To += addresses(h.Value)
		case "subject":
			m.Subject = decodeHeader(h.Value)
		case "x-zupit-automation":
			return model.Message{}, false, nil
		}
	}
	plain, rich, err := partText(v.Payload)
	if err != nil {
		return m, false, err
	}
	m.Body = plain
	if m.Body == "" {
		m.Body = htmlText(rich)
	}
	return m, true, nil
}
func partText(p gmailPart) (string, string, error) {
	if p.Filename != "" {
		return "", "", nil
	}
	plain, rich := "", ""
	if p.MimeType == "text/plain" || p.MimeType == "text/html" {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
		if err != nil {
			return "", "", errors.New("contenuto Gmail non decodificabile")
		}
		text := string(b)
		for _, h := range p.Headers {
			if strings.EqualFold(h.Name, "Content-Type") {
				_, params, _ := mime.ParseMediaType(h.Value)
				if label := params["charset"]; label != "" {
					reader, err := charset.NewReaderLabel(label, strings.NewReader(text))
					if err != nil {
						return "", "", errors.New("charset email non supportato")
					}
					decoded, err := io.ReadAll(io.LimitReader(reader, 2<<20))
					if err != nil {
						return "", "", err
					}
					text = string(decoded)
				}
			}
		}
		if p.MimeType == "text/plain" {
			plain = text
		} else {
			rich = text
		}
	}
	for _, child := range p.Parts {
		a, b, err := partText(child)
		if err != nil {
			return "", "", err
		}
		plain += a
		rich += b
	}
	if len(plain)+len(rich) > 2<<20 {
		return "", "", errors.New("testo email oltre il limite di 2 MiB")
	}
	return plain, rich, nil
}
func htmlText(value string) string {
	node, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "br" || n.Data == "p" || n.Data == "div" || n.Data == "tr") {
			b.WriteByte('\n')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && (strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://")) {
					b.WriteString(" (" + a.Val + ")")
				}
			}
		}
	}
	walk(node)
	return strings.TrimSpace(b.String())
}
func address(value string) string {
	if a, err := mail.ParseAddress(value); err == nil {
		return strings.ToLower(a.Address)
	}
	return strings.TrimSpace(value)
}
func addresses(value string) string {
	if list, err := mail.ParseAddressList(value); err == nil {
		var parts []string
		for _, a := range list {
			parts = append(parts, strings.ToLower(a.Address))
		}
		return strings.Join(parts, ", ")
	}
	return strings.TrimSpace(value)
}
func decodeHeader(value string) string {
	d := mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	if v, err := d.DecodeHeader(value); err == nil {
		return v
	}
	return value
}

// Sync uses Gmail history, not the order or completeness of Pub/Sub notifications.
func (c *Clients) Sync(ctx context.Context, conn model.Connection) error {
	unlock, err := c.Store.Lock(ctx, int64(2000000+conn.ID))
	if err != nil {
		return err
	}
	defer unlock()
	conn, err = c.Store.Connection(ctx, conn.ID)
	if err != nil {
		return err
	}
	if conn.Status != "ready" || conn.HistoryID == "" {
		return nil
	}
	client, err := c.gmailClient(ctx, conn.ID)
	if err != nil {
		return err
	}
	page := ""
	latest := conn.HistoryID
	for {
		var out struct {
			HistoryID string `json:"historyId"`
			Next      string `json:"nextPageToken"`
			History   []struct {
				Added []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
		}
		endpoint := gmailBase + "/history?historyTypes=messageAdded&maxResults=50&startHistoryId=" + url.QueryEscape(conn.HistoryID) + "&pageToken=" + url.QueryEscape(page)
		if err = c.do(ctx, client, "GET", endpoint, nil, nil, &out); err != nil {
			if f := Classify(err); strings.Contains(f.Message, "404") {
				return c.fullSync(ctx, conn, client)
			}
			return err
		}
		if out.HistoryID != "" {
			latest = out.HistoryID
		}
		var ids []string
		for _, h := range out.History {
			for _, added := range h.Added {
				ids = append(ids, added.Message.ID)
			}
		}
		if err = c.saveMessages(ctx, conn, client, ids, conn.HistoryID); err != nil {
			return err
		}
		if out.Next == "" {
			return c.Store.Ingest(ctx, conn.ID, nil, latest)
		}
		page = out.Next
	}
}
func (c *Clients) saveMessages(ctx context.Context, conn model.Connection, client *http.Client, ids []string, history string) error {
	var messages []model.Message
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		m, include, err := c.message(ctx, client, id)
		if err != nil {
			f := Classify(err)
			if strings.Contains(f.Message, "404") {
				if err = c.Store.Notify(ctx, fmt.Sprintf("missing:%d:%s", conn.ID, id), fmt.Sprintf("Casella #%d: email %s non più recuperabile dalla sorgente.", conn.ID, id)); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if include && !m.ReceivedAt.Before(conn.Cutover) {
			messages = append(messages, m)
		}
	}
	return c.Store.Ingest(ctx, conn.ID, messages, history)
}
func (c *Clients) fullSync(ctx context.Context, conn model.Connection, client *http.Client) error {
	var profile struct {
		HistoryID string `json:"historyId"`
	}
	if err := c.do(ctx, client, "GET", gmailBase+"/profile", nil, nil, &profile); err != nil {
		return err
	}
	page := ""
	for {
		var out struct {
			Next     string `json:"nextPageToken"`
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		}
		q := fmt.Sprintf("in:anywhere after:%d -in:sent -in:drafts -in:spam", conn.Cutover.Unix())
		endpoint := gmailBase + "/messages?includeSpamTrash=true&maxResults=50&q=" + url.QueryEscape(q) + "&pageToken=" + url.QueryEscape(page)
		if err := c.do(ctx, client, "GET", endpoint, nil, nil, &out); err != nil {
			return err
		}
		var ids []string
		for _, m := range out.Messages {
			ids = append(ids, m.ID)
		}
		if err := c.saveMessages(ctx, conn, client, ids, conn.HistoryID); err != nil {
			return err
		}
		if out.Next == "" {
			break
		}
		page = out.Next
	}
	if profile.HistoryID == "" {
		return errors.New("checkpoint Gmail assente")
	}
	return c.Store.Ingest(ctx, conn.ID, nil, profile.HistoryID)
}
func (c *Clients) forward(ctx context.Context, conn model.Connection, a model.Action) (model.Result, error) {
	if strings.ContainsAny(a.To+a.Subject, "\r\n") {
		return model.Result{}, errors.New("header inoltro non valido")
	}
	to, err := mail.ParseAddress(a.To)
	if err != nil {
		return model.Result{}, errors.New("destinatario inoltro non valido")
	}
	client, err := c.gmailClient(ctx, conn.ID)
	if err != nil {
		return model.Result{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\nX-Zupit-Automation: forward\r\n\r\n%s", conn.Config["email"], to.String(), mime.QEncoding.Encode("utf-8", a.Subject), base64.StdEncoding.EncodeToString([]byte(a.Body)))
	var out struct {
		ID string `json:"id"`
	}
	if err = c.do(ctx, client, "POST", gmailBase+"/messages/send", map[string]string{"raw": base64.RawURLEncoding.EncodeToString([]byte(b.String()))}, nil, &out); err != nil {
		return model.Result{}, err
	}
	if out.ID == "" {
		return model.Result{}, &Failure{Message: "Gmail non ha confermato l’invio", Uncertain: true}
	}
	return model.Result{Reference: "gmail:" + out.ID}, nil
}

// ParseEmail is shared by the RFC822 simulation/import boundary and parser tests.
func ParseEmail(raw string) (model.Message, error) {
	if len(raw) > 2<<20 {
		return model.Message{}, errors.New("email oltre il limite di 2 MiB")
	}
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return model.Message{}, errors.New("email RFC822 non valida")
	}
	body, err := mimeText(m.Header.Get("Content-Type"), m.Header.Get("Content-Transfer-Encoding"), m.Body)
	if err != nil {
		return model.Message{}, err
	}
	date, _ := m.Header.Date()
	if date.IsZero() {
		date = time.Now()
	}
	return model.Message{ID: m.Header.Get("Message-ID"), From: address(m.Header.Get("From")), To: addresses(m.Header.Get("To")), Subject: decodeHeader(m.Header.Get("Subject")), Body: body, ReceivedAt: date}, nil
}
func mimeText(contentType, transfer string, r io.Reader) (string, error) {
	typeName, params, err := mime.ParseMediaType(contentType)
	if contentType == "" {
		typeName = "text/plain"
		err = nil
	}
	if err != nil {
		return "", errors.New("tipo MIME non valido")
	}
	if strings.HasPrefix(typeName, "multipart/") {
		reader := multipart.NewReader(r, params["boundary"])
		var parts []string
		for {
			p, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", errors.New("multipart non valido")
			}
			if p.FileName() != "" {
				p.Close()
				continue
			}
			value, err := mimeText(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p)
			p.Close()
			if err != nil {
				return "", err
			}
			if value != "" {
				if typeName == "multipart/alternative" {
					if len(parts) == 0 || strings.HasPrefix(p.Header.Get("Content-Type"), "text/plain") {
						parts = []string{value}
					}
				} else {
					parts = append(parts, value)
				}
			}
		}
		return strings.Join(parts, "\n"), nil
	}
	if transfer == "base64" {
		r = base64.NewDecoder(base64.StdEncoding, r)
	} else if transfer == "quoted-printable" {
		r = quotedprintable.NewReader(r)
	}
	if label := params["charset"]; label != "" {
		r, err = charset.NewReaderLabel(label, r)
		if err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(io.LimitReader(r, 2<<20))
	if err != nil {
		return "", err
	}
	if typeName == "text/html" {
		return htmlText(string(b)), nil
	}
	if typeName != "text/plain" {
		return "", nil
	}
	return string(b), nil
}
