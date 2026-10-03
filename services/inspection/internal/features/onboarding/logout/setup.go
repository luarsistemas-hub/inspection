package logout

import (
	"errors"
	"net/http"
	"time"

	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
)

type Dependencies struct {
	Sessions     onboardingsession.Service
	SecureCookie func(*http.Request) bool
}

func Setup(mux *http.ServeMux, deps Dependencies) error {
	if mux == nil {
		return errors.New("onboarding logout: missing router")
	}
	if deps.Sessions.DB == nil {
		return errors.New("onboarding logout: missing session service")
	}
	if deps.SecureCookie == nil {
		return errors.New("onboarding logout: missing cookie policy")
	}
	mux.Handle("/onboarding/logout", handler{dependencies: deps})
	return nil
}

type handler struct{ dependencies Dependencies }

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("inspection_onboarding")
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		http.Error(w, "could not end the session", http.StatusBadRequest)
		return
	}
	if cookie != nil && cookie.Value != "" {
		csrf := r.Header.Get("X-CSRF-Token")
		if csrf == "" {
			http.Error(w, "could not end the session", http.StatusForbidden)
			return
		}
		if _, err := h.dependencies.Sessions.ValidateCSRF(r.Context(), cookie.Value, csrf); err != nil {
			code, _, _ := apperror.Public(err)
			if code == apperror.SessionExpired {
				h.clearCookie(w, r)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			status := http.StatusServiceUnavailable
			if code == apperror.Forbidden {
				status = http.StatusForbidden
			}
			http.Error(w, "could not end the session", status)
			return
		}
	}

	h.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h handler) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "inspection_onboarding",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.dependencies.SecureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}
