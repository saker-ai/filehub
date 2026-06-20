package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/saker-ai/assethub/pkg/config"
	"github.com/saker-ai/saker-common/internaljwt"
	"golang.org/x/time/rate"
)

const tenantKey = "tenant_id"
const assetIDKey = "asset_id"
const scopesKey = "scopes"

func CORSMiddleware(origins []string) gin.HandlerFunc {
	allowAll := len(origins) == 0
	for _, origin := range origins {
		if origin == "*" {
			allowAll = true
			break
		}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			for _, allowed := range origins {
				if origin == allowed {
					c.Header("Access-Control-Allow-Origin", origin)
					c.Header("Vary", "Origin")
					break
				}
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, X-API-Key, Content-Type, X-Request-ID")
		c.Header("Access-Control-Max-Age", "43200")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if strings.TrimSpace(id) == "" {
			id = uuid.NewString()
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"request_id", c.Writer.Header().Get("X-Request-ID"),
			"tenant_id", Tenant(c),
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if id, ok := c.Get(assetIDKey); ok {
			attrs = append(attrs, "asset_id", id)
		} else if id := c.Param("id"); id != "" {
			attrs = append(attrs, "asset_id", id)
		}
		if c.Writer.Status() >= 400 {
			errText := http.StatusText(c.Writer.Status())
			if len(c.Errors) > 0 {
				errText = c.Errors.String()
			}
			attrs = append(attrs, "error", errText)
		}
		logger.InfoContext(c.Request.Context(), "assethub request", attrs...)
	}
}

func MetricsMiddleware(metrics *Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		metrics.RecordRequest(c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start))
	}
}

func Auth(cfg config.Config) gin.HandlerFunc {
	var verifier *internaljwt.Verifier
	if cfg.InternalAuth.Enabled {
		v, err := internaljwt.NewVerifier(internaljwt.VerifierOptions{
			Issuer:                     cfg.InternalAuth.Issuer,
			Audience:                   cfg.InternalAuth.Audience,
			MasterSecret:               cfg.InternalAuth.MasterSecret,
			TTL:                        cfg.InternalAuth.TTL,
			ClockSkew:                  cfg.InternalAuth.ClockSkew,
			AllowAuthorizationFallback: cfg.InternalAuth.AllowAuthorizationFallback,
		})
		if err != nil {
			return func(c *gin.Context) {
				writeError(c, http.StatusInternalServerError, "internal_auth_config_error", "internal auth misconfigured")
				c.Abort()
			}
		}
		verifier = v
	}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/v1/") || strings.HasPrefix(c.Request.URL.Path, "/v1/dl/") {
			c.Next()
			return
		}
		if verifier != nil {
			principal, err := verifier.VerifyRequest(c.Request)
			if err == nil {
				if !scopeAllows(principal.Scopes, c.Request.Method) {
					writeError(c, http.StatusForbidden, "insufficient_scope", "insufficient scope")
					c.Abort()
					return
				}
				c.Set(tenantKey, principal.TenantID)
				c.Set(scopesKey, principal.Scopes)
				c.Next()
				return
			}
			if !errors.Is(err, internaljwt.ErrMissingToken) {
				writeError(c, http.StatusUnauthorized, "authentication_required", "authentication required")
				c.Abort()
				return
			}
		}
		if cfg.APIKeyAuthEnabled {
			key := c.GetHeader("X-API-Key")
			if key == "" {
				auth := c.GetHeader("Authorization")
				if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
					key = strings.TrimSpace(auth[len("Bearer "):])
				}
			}
			for _, allowed := range cfg.APIKeys {
				if subtle.ConstantTimeCompare([]byte(key), []byte(allowed)) == 1 {
					c.Set(tenantKey, "default")
					c.Next()
					return
				}
			}
			writeError(c, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
			c.Abort()
			return
		}
		writeError(c, http.StatusUnauthorized, "authentication_required", "authentication required")
		c.Abort()
	}
}

func scopeAllows(scopes []string, method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return internaljwt.HasAnyScope(scopes,
			internaljwt.ScopeAssetHubRead,
			internaljwt.ScopeAssetHubUpload,
			internaljwt.ScopeAssetHubWrite,
			internaljwt.ScopeAssetHubAdmin,
		)
	case http.MethodPost, http.MethodPut:
		return internaljwt.HasAnyScope(scopes,
			internaljwt.ScopeAssetHubUpload,
			internaljwt.ScopeAssetHubWrite,
			internaljwt.ScopeAssetHubAdmin,
		)
	case http.MethodPatch, http.MethodDelete:
		return internaljwt.HasAnyScope(scopes,
			internaljwt.ScopeAssetHubWrite,
			internaljwt.ScopeAssetHubAdmin,
		)
	default:
		return false
	}
}

func Tenant(c *gin.Context) string {
	if v, ok := c.Get(tenantKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "default"
}

func RateLimit(perSec, burst int) gin.HandlerFunc {
	if perSec <= 0 {
		perSec = 100
	}
	if burst <= 0 {
		burst = 200
	}
	lim := rate.NewLimiter(rate.Limit(perSec), burst)
	var mu sync.Mutex
	return func(c *gin.Context) {
		mu.Lock()
		remaining := int(lim.Tokens())
		mu.Unlock()
		c.Header("X-RateLimit-Limit", strconvI(perSec))
		c.Header("X-RateLimit-Remaining", strconvI(remaining))
		c.Header("X-RateLimit-Reset", strconvI(int(time.Now().Add(time.Second).Unix())))
		if !lim.Allow() {
			writeError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "rate limit exceeded")
			c.Abort()
			return
		}
		c.Next()
	}
}

func strconvI(v int) string {
	return strconv.FormatInt(int64(v), 10)
}
