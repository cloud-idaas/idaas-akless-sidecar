// Package health checks local HTTP availability independently of IDaaS and cloud permissions.
package health

import "net/http"

// HealthzHandler reports local HTTP health, not upstream or cloud permissions.
func HealthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
