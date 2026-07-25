package gossr

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

func setSSRDataNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", cacheNoStoreHTML)
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func ssrDataNoStoreMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		setSSRDataNoStoreHeaders(c)
		c.Next()
	}
}

func writeNavigationError(c *gin.Context, status int, code, message string) {
	setSSRDataNoStoreHeaders(c)
	c.JSON(status, navigationErrorOutcome{
		Kind:    "error",
		Status:  status,
		Code:    code,
		Message: message,
	})
}

func ssrGuardMiddleware(authorizer SSRFetchAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if code, ok := authorizeSSRFetch(c.Request, authorizer); !ok {
			c.AbortWithStatus(code)
			return
		}

		c.Next()
	}
}

func authorizeSSRFetch(r *http.Request, authorizer SSRFetchAuthorizer) (int, bool) {
	if r == nil {
		return http.StatusForbidden, false
	}
	if authorizer == nil {
		authorizer = DefaultSSRFetchAuthorizer
	}

	status, allowed := authorizer(r)
	if allowed {
		return 0, true
	}
	if status < 400 || status > 499 {
		status = http.StatusForbidden
	}
	return status, false
}

// DefaultSSRFetchAuthorizer 使用浏览器 Origin/Referer 或 Sec-Fetch-Site
// 验证同源请求。自定义授权器可调用它并叠加宿主身份校验。
func DefaultSSRFetchAuthorizer(r *http.Request) (int, bool) {
	if sameOriginRequest(r) {
		return 0, true
	}
	return http.StatusForbidden, false
}

func configuredSSRFetchAuthorizer(
	authorizer SSRFetchAuthorizer,
	configuredSiteOrigin string,
	alternateOrigins ...string,
) SSRFetchAuthorizer {
	if authorizer != nil {
		return authorizer
	}

	targetOrigins := make([]string, 0, 1+len(alternateOrigins))
	addTargetOrigin := func(raw string) {
		if raw == "" {
			return
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return
		}
		targetOrigin, ok := canonicalOrigin(parsed.Scheme, parsed.Host)
		if !ok {
			return
		}
		for _, existing := range targetOrigins {
			if existing == targetOrigin {
				return
			}
		}
		targetOrigins = append(targetOrigins, targetOrigin)
	}
	addTargetOrigin(configuredSiteOrigin)
	for _, alternateOrigin := range alternateOrigins {
		addTargetOrigin(alternateOrigin)
	}
	if len(targetOrigins) == 0 {
		return authorizer
	}

	return func(r *http.Request) (int, bool) {
		if configuredSiteOrigin == "" && sameOriginRequest(r) {
			return 0, true
		}
		for _, targetOrigin := range targetOrigins {
			if sameOriginRequestAgainst(r, targetOrigin) {
				return 0, true
			}
		}
		return http.StatusForbidden, false
	}
}

func sameOriginRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	targetOrigin, ok := canonicalOrigin(requestScheme(r), primaryHost(r))
	if !ok {
		return false
	}
	return sameOriginRequestAgainst(r, targetOrigin)
}

func sameOriginRequestAgainst(r *http.Request, targetOrigin string) bool {
	if r == nil || targetOrigin == "" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	sourceOrigin, ok := canonicalOrigin(parsed.Scheme, parsed.Host)
	if !ok {
		return false
	}

	return sourceOrigin == targetOrigin
}

func ensureLeadingSlash(rawPath string) string {
	if rawPath == "" {
		return "/"
	}
	if strings.HasPrefix(rawPath, "/") {
		return rawPath
	}
	return "/" + rawPath
}
