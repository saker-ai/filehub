package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/assethub/pkg/store"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

func writeError(c *gin.Context, status int, code, message string) {
	typ := "invalid_request_error"
	if status == http.StatusUnauthorized {
		typ = "authentication_error"
	} else if status >= 500 {
		typ = "server_error"
	}
	c.JSON(status, errorBody{Error: errorDetail{Message: message, Type: typ, Code: code, RequestID: c.Writer.Header().Get("X-Request-ID")}})
}

func writeDuplicateError(c *gin.Context, assetID string) {
	c.JSON(http.StatusConflict, gin.H{"error": gin.H{
		"message":    "duplicate asset",
		"type":       "invalid_request_error",
		"code":       "conflict",
		"asset_id":   assetID,
		"request_id": c.Writer.Header().Get("X-Request-ID"),
	}})
}

func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(c, http.StatusConflict, "conflict", "resource conflict")
	case errors.Is(err, store.ErrForbidden):
		writeError(c, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, store.ErrQuotaExceeded):
		writeError(c, http.StatusRequestEntityTooLarge, "storage_quota_exceeded", "storage quota exceeded")
	default:
		writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
