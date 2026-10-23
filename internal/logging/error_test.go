package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorsExposeUsefulCodesWithoutRawDetails(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"disk", &pgconn.PgError{Code: "53100", Message: "PRIVATE", Detail: "PRIVATE"}, `"sqlstate":"53100"`},
		{"password", &pgconn.PgError{Code: "28P01", Message: "PRIVATE"}, "DATABASE_URL"},
		{"connection", &url.Error{Op: "Get", URL: "https://user:PRIVATE@example.test/?token=PRIVATE", Err: syscall.ECONNREFUSED}, "connection_refused"},
		{"dns", &net.DNSError{Err: "PRIVATE", Name: "PRIVATE"}, `"kind":"dns"`},
		{"file", &os.PathError{Op: "open", Path: "PRIVATE", Err: os.ErrPermission}, "UID 10001"},
		{"oauth", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 401}, Body: []byte("PRIVATE")}, `"http_status":401`},
		{"pubsub", status.Error(codes.PermissionDenied, "PRIVATE"), `"grpc_code":"PermissionDenied"`},
		{"timeout", context.DeadlineExceeded, `"kind":"timeout"`},
		{"unknown", errors.New("PRIVATE"), `"kind":"internal"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&out, nil))
			logger.Error("Operazione fallita", Error(fmt.Errorf("PRIVATE: %w", tt.err)))
			if text := out.String(); strings.Contains(text, "PRIVATE") || !strings.Contains(text, tt.want) {
				t.Fatalf("unsafe or unhelpful log: %s", text)
			}
		})
	}
}
