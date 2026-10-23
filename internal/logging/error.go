package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/status"
)

// Error records diagnostic codes, never raw errors that may contain credentials or payloads.
func Error(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	var safe slog.LogValuer
	if errors.As(err, &safe) {
		return slog.Any("error", safe)
	}
	var pg *pgconn.PgError
	var oauth *oauth2.RetrieveError
	var dns *net.DNSError
	kind, hint := "internal", "Controllare il dettaglio dell’operazione nell’applicazione"
	attrs := []any{"type", fmt.Sprintf("%T", err)}
	switch {
	case errors.Is(err, context.Canceled):
		kind, hint = "canceled", "Operazione annullata o arresto del servizio"
	case errors.Is(err, context.DeadlineExceeded):
		kind, hint = "timeout", "Verificare disponibilità del servizio e connettività"
	case errors.As(err, &pg):
		kind, hint = "postgres", "Controllare i log PostgreSQL e lo stato del database"
		attrs = append(attrs, "sqlstate", pg.Code)
		switch pg.Code {
		case "53100":
			hint = "Spazio disco esaurito: liberare spazio senza eliminare il volume PostgreSQL"
		case "28P01", "28000":
			hint = "Verificare utente e password della DATABASE_URL"
		case "3D000":
			hint = "Verificare che il database configurato esista"
		case "42501":
			hint = "Verificare i permessi del ruolo PostgreSQL"
		case "53300":
			hint = "Limite connessioni PostgreSQL raggiunto"
		}
	case errors.As(err, &oauth):
		kind, hint = "oauth", "Verificare autorizzazione Google e credenziali del client"
		if oauth.Response != nil {
			attrs = append(attrs, "http_status", oauth.Response.StatusCode)
		}
	case errors.As(err, &dns):
		kind, hint = "dns", "Verificare DNS e risoluzione dei nomi nel container"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind, hint = "connection_refused", "Verificare host, porta e servizio in ascolto; per il database controllare i log PostgreSQL"
	case errors.Is(err, os.ErrPermission):
		kind, hint = "permission", "Verificare permessi dei file e accesso ai secret per UID 10001"
	case errors.Is(err, os.ErrNotExist):
		kind, hint = "not_found", "Verificare percorsi e mount dei file richiesti"
	default:
		if grpc, ok := status.FromError(err); ok {
			kind, hint = "grpc", "Verificare credenziali ADC, subscription Pub/Sub e permessi dell’identità runtime"
			attrs = append(attrs, "grpc_code", grpc.Code().String())
		} else {
			var network net.Error
			if errors.As(err, &network) {
				kind, hint = "network", "Verificare connettività, firewall e disponibilità del servizio"
			}
		}
	}
	attrs = append(attrs, "kind", kind, "hint", hint)
	return slog.Group("error", attrs...)
}
