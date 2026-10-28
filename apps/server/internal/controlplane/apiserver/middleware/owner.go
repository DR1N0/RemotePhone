package middleware

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"remotephone/internal/controlplane/auth"
)

// RequireOwner protects the loopback admin surface. A request without a
// bearer token is accepted only from loopback with a JSON content type and no
// external Origin; it is attributed to the reserved _SYSTEM actor. A request
// with a bearer token still uses the normal account authentication path, so
// existing programmatic admin clients keep working.
func RequireOwner(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header := r.Header.Get("Authorization"); strings.HasPrefix(header, bearerPrefix) {
				token := strings.TrimPrefix(header, bearerPrefix)
				if token == "" {
					WriteJSONError(w, http.StatusUnauthorized, "missing bearer token")
					return
				}
				user, err := svc.Authenticate(r.Context(), token)
				if err != nil {
					panic(err)
				}
				if user == nil {
					WriteJSONError(w, http.StatusUnauthorized, "invalid or revoked credential")
					return
				}
				if user.Scope != "account" {
					WriteJSONError(w, http.StatusForbidden, "credential scope is not authorized")
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authenticatedUserKey, user)))
				return
			}

			if !isLoopbackHost(r.Host) {
				WriteJSONError(w, http.StatusForbidden, "owner API is loopback-only")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
				WriteJSONError(w, http.StatusForbidden, "browser origins are not allowed")
				return
			}
			if r.Method == http.MethodPost &&
				!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				WriteJSONError(w, http.StatusUnsupportedMediaType, "application/json required")
				return
			}
			owner := &auth.AuthenticatedUser{
				UserID:       auth.SystemUserID,
				CredentialID: auth.SystemCredentialID,
				Scope:        "account",
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authenticatedUserKey, owner)))
		})
	}
}

func isLoopbackHost(host string) bool {
	host = stripPort(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return isLoopbackHost(u.Host)
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}
