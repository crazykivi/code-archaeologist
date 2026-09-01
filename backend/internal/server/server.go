package server

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/handlers"
	"code-archaeologist/backend/internal/middleware"
)

func New(cfg *config.Config, api *handlers.API) *gin.Engine {
	switch cfg.GinMode {
	case gin.ReleaseMode:
		gin.SetMode(gin.ReleaseMode)
	case gin.TestMode:
		gin.SetMode(gin.TestMode)
	default:
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.Use(bodyLimit(1 << 20))
	r.Use(middleware.CORS(cfg.AllowOrigins))

	if len(cfg.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
			log.Printf("[Server] failed to set trusted proxies: %v", err)
		}
	} else {
		if err := r.SetTrustedProxies(nil); err != nil {
			log.Printf("[Server] failed to disable trusted proxies: %v", err)
		}
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	analyzeLimiter := middleware.NewRateLimiter(cfg.RateLimitAnalyzePerMinute, 3)
	readLimiter := middleware.NewRateLimiter(cfg.RateLimitReadPerMinute, 100)

	v1 := r.Group("/api/v1")
	v1.POST("/analyze", middleware.RateLimit(analyzeLimiter), api.Analyze)
	v1.GET("/jobs", middleware.RateLimit(readLimiter), api.Jobs)
	v1.GET("/jobs/:id", middleware.RateLimit(readLimiter), api.Job)
	v1.DELETE("/jobs/:id", middleware.RateLimit(readLimiter), api.DeleteJob)
	v1.GET("/reports/:id", middleware.RateLimit(readLimiter), api.Report)
	v1.GET("/providers", middleware.RateLimit(readLimiter), api.Providers)

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	return r
}

func bodyLimit(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}
