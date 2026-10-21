package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
)

// localOriginOnly rejects requests that did not come from the local web UI or CLI.
//
// The Host check defeats DNS rebinding (an attacker domain resolving to 127.0.0.1),
// and the Origin check defeats cross-site requests from other pages open in the
// user's browser, which could otherwise create comments that end up in Claude's
// context via /cr-address.
func localOriginOnly(port string) func(http.Handler) http.Handler {
	allowedHosts := map[string]bool{
		"localhost:" + port: true,
		"127.0.0.1:" + port: true,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !allowedHosts[r.Host] {
				http.Error(w, "Forbidden host", http.StatusForbidden)
				return
			}

			// Browsers send Origin on cross-origin requests and on all non-GET
			// requests; the CLI sends none.
			if origin := r.Header.Get("Origin"); origin != "" {
				if !strings.HasPrefix(origin, "http://") || !allowedHosts[strings.TrimPrefix(origin, "http://")] {
					http.Error(w, "Forbidden origin", http.StatusForbidden)
					return
				}
			}

			// Default CSP: only scripts served by us. The viewer overrides this
			// with a nonce for its inline bootstrap script.
			w.Header().Set("Content-Security-Policy", contentSecurityPolicy(""))
			next.ServeHTTP(w, r)
		})
	}
}

// contentSecurityPolicy blocks inline scripts and event handlers, so raw HTML in
// rendered Markdown cannot execute JavaScript. A non-empty nonce allows the
// matching inline <script> tag.
func contentSecurityPolicy(nonce string) string {
	scriptSrc := "script-src 'self'"
	if nonce != "" {
		scriptSrc += " 'nonce-" + nonce + "'"
	}
	return scriptSrc + "; object-src 'none'; base-uri 'none'; form-action 'self'"
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isWithinDir reports whether path is dir itself or located inside it.
func isWithinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
