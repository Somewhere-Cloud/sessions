package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAccountPreference struct {
	sessionService
	calls int
}

func (f *fakeAccountPreference) SetDefaultAccount(tool, name string) error { f.calls++; return nil }

func TestAccountPreferenceRequiresAuthorityAndExplicitName(t *testing.T) {
	for _, test := range []struct {
		body  string
		admin bool
		code  int
	}{
		{`{"tool":"claude","name":"bean"}`, false, 403},
		{`{"tool":"claude","name":"bean"}`, true, 200},
		{`{"tool":"claude","name":""}`, true, 200},
		{`{"tool":"claude"}`, true, 400},
	} {
		service := &fakeAccountPreference{}
		server := &Server{registry: service}
		r := httptest.NewRequest("PUT", "/api/account-default", strings.NewReader(test.body))
		r = r.WithContext(context.WithValue(r.Context(), authPrincipalContextKey{}, authPrincipal{HostAdmin: test.admin}))
		w := httptest.NewRecorder()
		if !server.handleProfilesRoute(w, r, "") || w.Code != test.code {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body.String())
		}
		if test.code != 200 && service.calls != 0 {
			t.Fatal("unauthorized mutation")
		}
	}
}
