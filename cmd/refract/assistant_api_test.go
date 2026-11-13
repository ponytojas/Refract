package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ponytojas/Refract/internal/assistant"
)

// A second accept or reject while one is in progress is a conflict, not a
// server error.
func TestAssistantErrorReviewing(t *testing.T) {
	for _, err := range []error{assistant.ErrReviewing, assistant.ErrBusy, assistant.ErrState} {
		w := httptest.NewRecorder()
		assistantError(w, err)
		if w.Code != http.StatusConflict {
			t.Errorf("%v: status %d, want 409", err, w.Code)
		}
	}
}
