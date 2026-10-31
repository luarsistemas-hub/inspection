package testfailure

import (
	"net/http/httptest"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFailureInjectionAtDatabaseBoundary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(db); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode string
		want apperror.Code
	}{
		{mode: "dependency", want: apperror.DependencyUnavailable},
		{mode: "internal", want: apperror.Internal},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/graphql", strings.NewReader(`query { externalCapture { status } }`))
			r.Header.Set(HeaderOperation, "externalCapture")
			r.Header.Set(HeaderFailure, tc.mode)
			ctx := WithRequest(r, "test").Context()
			err := db.WithContext(ctx).Table("media.video_policies").Find(&[]struct{}{}).Error
			if err == nil {
				t.Fatal("expected injected database error")
			}
			code, _, safe := apperror.Public(err)
			if code != tc.want {
				t.Fatalf("code = %s, want %s (error %v)", code, tc.want, err)
			}
			if tc.mode == "internal" && (safe != "Não foi possível concluir a operação." || strings.Contains(safe, Secret)) {
				t.Fatalf("internal error was not safely presented: %q", safe)
			}
		})
	}
}

func TestFailureInjectionWaitsUntilAuthenticationTablesAreRead(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE external_sessions (id integer primary key)").Error; err != nil {
		t.Fatal(err)
	}
	if err := Install(db); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/graphql", nil)
	r.Header.Set(HeaderOperation, "externalCapture")
	r.Header.Set(HeaderFailure, "dependency")
	ctx := WithRequest(r, "test").Context()
	if err := db.WithContext(ctx).Table("external_sessions").Find(&[]struct{}{}).Error; err != nil {
		t.Fatalf("authentication session lookup was interrupted: %v", err)
	}
	err = db.WithContext(ctx).Table("media.video_policies").Find(&[]struct{}{}).Error
	if err == nil {
		t.Fatal("expected slice database boundary failure")
	}
	code, _, _ := apperror.Public(err)
	if code != apperror.DependencyUnavailable {
		t.Fatalf("slice error code = %s, want %s (%v)", code, apperror.DependencyUnavailable, err)
	}
}

func TestFailureInjectionIgnoredOutsideTestEnvironment(t *testing.T) {
	for _, env := range []string{"local", "production", "staging", ""} {
		t.Run(env, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/graphql", nil)
			r.Header.Set(HeaderOperation, "externalCapture")
			r.Header.Set(HeaderFailure, "internal")
			ctx := WithRequest(r, env).Context()
			if _, ok := ctx.Value(contextKey{}).(failure); ok {
				t.Fatal("failure hook activated outside test environment")
			}
		})
	}
}

func TestFailureInjectionRejectsUnknownOperationAndMode(t *testing.T) {
	for _, tc := range []struct{ operation, mode string }{{"unknown", "internal"}, {"externalCapture", "anything"}} {
		r := httptest.NewRequest("POST", "/graphql", nil)
		r.Header.Set(HeaderOperation, tc.operation)
		r.Header.Set(HeaderFailure, tc.mode)
		ctx := WithRequest(r, "test").Context()
		if _, ok := ctx.Value(contextKey{}).(failure); ok {
			t.Fatalf("hook activated for operation %q mode %q", tc.operation, tc.mode)
		}
	}
}
