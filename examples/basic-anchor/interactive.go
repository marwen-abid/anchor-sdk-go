package main

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"

	"github.com/marwen-abid/anchor-sdk-go/anchor"
)

//go:embed templates/interactive.html
var interactiveTemplate embed.FS

// interactiveData is the data passed to the HTML template
type interactiveData struct {
	Amount string
}

// successData is the success response data
type successData struct {
	Message string `json:"message"`
}

// errorResponse is an error response
type errorResponse struct {
	Error string `json:"error"`
}

// handleGetInteractive displays the interactive KYC form.
// GET /interactive?token={token}
func handleGetInteractive(tm *anchor.TransferManager) http.HandlerFunc {
	// Parse template once at startup
	tmpl := template.Must(template.ParseFS(interactiveTemplate, "templates/interactive.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, `{"error":"missing token parameter"}`, http.StatusBadRequest)
			return
		}

		// Peek token without consuming it (GET displays form, POST consumes)
		transfer, err := tm.PeekInteractiveToken(context.Background(), token)
		if err != nil {
			log.Printf("Invalid token: %v", err)
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		// Render template with transfer amount
		data := interactiveData{
			Amount: transfer.Amount,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Failed to render template: %v", err)
		}
	}
}

// handlePostInteractive processes the interactive KYC form submission.
// POST /interactive with form data containing name, email, and token
func handlePostInteractive(tm *anchor.TransferManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			log.Printf("Failed to parse form: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid form data"})
			return
		}

		token := r.FormValue("token")
		name := r.FormValue("name")
		email := r.FormValue("email")

		// Validate required fields
		if token == "" || name == "" || email == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(errorResponse{Error: "missing required fields"})
			return
		}

		// Consume token to get transfer ID (one-time use on POST)
		transfer, err := tm.ConsumeInteractiveToken(context.Background(), token)
		if err != nil {
			log.Printf("Invalid token on POST: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(errorResponse{Error: "invalid token"})
			return
		}

		// Complete interactive flow with KYC data
		kyeData := map[string]any{
			"name":  name,
			"email": email,
		}

		if err := tm.CompleteInteractive(context.Background(), transfer.ID, kyeData); err != nil {
			log.Printf("Failed to complete interactive: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(errorResponse{Error: "failed to process transfer"})
			return
		}

		// Return success response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(successData{Message: "Transfer initiated successfully"})
	}
}
