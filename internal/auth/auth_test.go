package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeVerifier struct {
	principal Principal
	err       error
}

func (f fakeVerifier) Verify(context.Context, string) (Principal, error) {
	return f.principal, f.err
}

func serve(t *testing.T, v TokenVerifier, role, authorization string) (int, bool) {
	t.Helper()

	called := false

	handler := Require(v, role, http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		},
	))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, called
}

func TestRequire(t *testing.T) {
	ok := fakeVerifier{principal: Principal{
		ProviderID: "provider-a",
		Roles:      []string{RoleProvider},
	}}
	bad := fakeVerifier{err: ErrInvalidToken}

	tests := []struct {
		name       string
		verifier   TokenVerifier
		role       string
		header     string
		wantStatus int
		wantCalled bool
	}{
		{"sem credenciais", ok, RoleProvider, "", 401, false},
		{"esquema errado", ok, RoleProvider, "Basic abc", 401, false},
		{"token vazio", ok, RoleProvider, "Bearer ", 401, false},
		{"token invalido ou expirado", bad, RoleProvider, "Bearer x", 401, false},
		{"papel insuficiente", ok, RoleInternal, "Bearer x", 403, false},
		{"autorizado", ok, RoleProvider, "Bearer x", 204, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, called := serve(t, tt.verifier, tt.role, tt.header)

			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}

			if called != tt.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}

func TestPrincipalInContext(t *testing.T) {
	want := Principal{ProviderID: "provider-a"}

	got, ok := PrincipalFrom(WithPrincipal(context.Background(), want))
	if !ok || got.ProviderID != want.ProviderID {
		t.Fatalf("principal = %+v, ok = %v", got, ok)
	}

	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("expected no principal in empty context")
	}
}
