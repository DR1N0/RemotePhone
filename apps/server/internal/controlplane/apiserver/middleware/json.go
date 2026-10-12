package middleware

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes payload as a JSON response body with the given status
// code and a Content-Type of application/json. This is the one function
// every route handler uses for a success response — no copy-pasted
// json.Marshal calls per route package.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteJSONError writes a {"error": message} JSON body with the given
// status code and a Content-Type of application/json. This is the one
// function both this package's middleware and every route handler use for
// an error response — no duplicate JSON-error-writing logic anywhere else.
func WriteJSONError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}
