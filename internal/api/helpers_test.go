package api_test

import (
	"net/http/httptest"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
)

// newAPITestServerFromApp mounts the app's full route table on an
// httptest server (the auth/write-gate tests and the fee/panel tests share
// it — one wiring, one cleanup).
func newAPITestServerFromApp(t *testing.T, a *app.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(srv.Close)
	return srv
}
