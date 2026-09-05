package httpapi

import (
	"fmt"
	"net/http"
	"time"
)

type responseMetricsWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseMetricsWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseMetricsWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(body)
	w.bytes += written
	return written, err
}

func (w *responseMetricsWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (h *Handler) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		metrics := &responseMetricsWriter{ResponseWriter: w}
		defer func() {
			status := metrics.status
			if status == 0 {
				status = http.StatusOK
			}
			h.logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", metrics.bytes,
				"duration", time.Since(startedAt),
			)
		}()
		next.ServeHTTP(metrics, r)
	})
}

func (h *Handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.logger.Error("http panic recovered", "error", fmt.Sprint(recovered))
				if metrics, ok := w.(*responseMetricsWriter); !ok || metrics.status == 0 {
					writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) responseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
