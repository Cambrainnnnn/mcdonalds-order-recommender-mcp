package httpapi

import (
	"fmt"
	"net/http"
	"time"
)

func (h *Handler) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(w, r)
		h.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(startedAt),
		)
	})
}

func (h *Handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.logger.Error("http panic recovered", "error", fmt.Sprint(recovered))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
