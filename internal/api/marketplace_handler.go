package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"aiops/internal/capability"
)

// MarketplaceHandler handles capability marketplace endpoints
type MarketplaceHandler struct {
	service *capability.MarketplaceService
}

func NewMarketplaceHandler(service *capability.MarketplaceService) *MarketplaceHandler {
	return &MarketplaceHandler{service: service}
}

// RegisterRoutes registers marketplace endpoints
func (h *MarketplaceHandler) RegisterRoutes(r *gin.RouterGroup) {
	marketplace := r.Group("/marketplace")
	{
		// Capability registry
		marketplace.POST("/capabilities", h.PublishCapability)
		marketplace.GET("/capabilities", h.SearchCapabilities)
		marketplace.GET("/capabilities/:id", h.GetCapability)
		marketplace.GET("/capabilities/:id/versions", h.GetCapabilityVersions)
		marketplace.GET("/capabilities/:id/download/:version_id", h.DownloadCapability)

		// Ratings
		marketplace.POST("/capabilities/:id/ratings", h.RateCapability)
		marketplace.GET("/capabilities/:id/ratings", h.GetCapabilityRatings)

		// Stats
		marketplace.GET("/capabilities/:id/stats", h.GetCapabilityStats)
	}
}

// PublishCapability godoc
// @Summary Publish a new capability or version
// @Tags marketplace
// @Accept json
// @Produce json
// @Param request body capability.PublishCapabilityRequest true "Publish request"
// @Success 200 {object} map[string]interface{}
// @Router /api/marketplace/capabilities [post]
func (h *MarketplaceHandler) PublishCapability(c *gin.Context) {
	var req capability.PublishCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user from JWT
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
		return
	}
	req.OwnerID = userID.(string)

	registry, version, err := h.service.PublishCapability(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"capability": registry,
		"version":    version,
	})
}

// SearchCapabilities godoc
// @Summary Search capabilities in marketplace
// @Tags marketplace
// @Accept json
// @Produce json
// @Param query query string false "Search query"
// @Param domain query string false "Filter by domain"
// @Param category query string false "Filter by category"
// @Param risk_level query string false "Filter by risk level"
// @Param min_rating query number false "Minimum rating"
// @Param visibility query string false "Filter by visibility"
// @Param sort_by query string false "Sort by: downloads, rating, created_at"
// @Param limit query int false "Result limit"
// @Param offset query int false "Result offset"
// @Success 200 {object} map[string]interface{}
// @Router /api/marketplace/capabilities [get]
func (h *MarketplaceHandler) SearchCapabilities(c *gin.Context) {
	req := capability.SearchCapabilitiesRequest{
		Query:      c.Query("query"),
		Domain:     c.Query("domain"),
		Category:   c.Query("category"),
		RiskLevel:  c.Query("risk_level"),
		Visibility: c.Query("visibility"),
		Status:     c.Query("status"),
		SortBy:     c.Query("sort_by"),
	}

	if minRating := c.Query("min_rating"); minRating != "" {
		rating, err := strconv.ParseFloat(minRating, 64)
		if err == nil {
			req.MinRating = &rating
		}
	}

	if limit := c.Query("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			req.Limit = l
		}
	}

	if offset := c.Query("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil {
			req.Offset = o
		}
	}

	capabilities, total, err := h.service.SearchCapabilities(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"capabilities": capabilities,
		"total":        total,
		"limit":        req.Limit,
		"offset":       req.Offset,
	})
}

// GetCapability godoc
// @Summary Get capability details
// @Tags marketplace
// @Produce json
// @Param id path string true "Capability ID"
// @Success 200 {object} capability.CapabilityRegistry
// @Router /api/marketplace/capabilities/{id} [get]
func (h *MarketplaceHandler) GetCapability(c *gin.Context) {
	id := c.Param("id")
	cap, err := h.service.GetCapability(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "capability not found"})
		return
	}

	c.JSON(http.StatusOK, cap)
}

// GetCapabilityVersions godoc
// @Summary Get all versions of a capability
// @Tags marketplace
// @Produce json
// @Param id path string true "Capability ID"
// @Success 200 {array} capability.CapabilityVersion
// @Router /api/marketplace/capabilities/{id}/versions [get]
func (h *MarketplaceHandler) GetCapabilityVersions(c *gin.Context) {
	id := c.Param("id")
	versions, err := h.service.GetCapabilityVersions(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// DownloadCapability godoc
// @Summary Download a capability version
// @Tags marketplace
// @Produce json
// @Param id path string true "Capability ID"
// @Param version_id path string true "Version ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/marketplace/capabilities/{id}/download/{version_id} [get]
func (h *MarketplaceHandler) DownloadCapability(c *gin.Context) {
	capabilityID := c.Param("id")
	versionID := c.Param("version_id")

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
		return
	}

	// Get version
	versions, err := h.service.GetCapabilityVersions(c.Request.Context(), capabilityID)
	if err != nil || len(versions) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}

	var targetVersion *capability.CapabilityVersion
	for _, v := range versions {
		if v.ID == versionID {
			targetVersion = v
			break
		}
	}

	if targetVersion == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}

	// Record download
	organizationID, _ := c.Get("organization_id")
	environment, _ := c.Get("environment")
	orgIDStr, _ := organizationID.(*string)
	envStr, _ := environment.(*string)

	err = h.service.RecordDownload(
		c.Request.Context(),
		capabilityID,
		versionID,
		userID.(string),
		orgIDStr,
		envStr,
		"api",
	)
	if err != nil {
		// Log but don't fail the download
		c.Error(err)
	}

	c.JSON(http.StatusOK, gin.H{
		"yaml_content": targetVersion.YAMLContent,
		"version":      targetVersion.Version,
		"download_url": "/api/marketplace/capabilities/" + capabilityID + "/download/" + versionID,
	})
}

// RateCapability godoc
// @Summary Rate a capability
// @Tags marketplace
// @Accept json
// @Produce json
// @Param id path string true "Capability ID"
// @Param request body map[string]interface{} true "Rating request"
// @Success 200 {object} map[string]string
// @Router /api/marketplace/capabilities/{id}/ratings [post]
func (h *MarketplaceHandler) RateCapability(c *gin.Context) {
	capabilityID := c.Param("id")

	var req struct {
		Rating      int     `json:"rating" binding:"required,min=1,max=5"`
		Review      *string `json:"review"`
		VersionUsed *string `json:"version_used"`
		Environment *string `json:"environment"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
		return
	}

	err := h.service.RateCapability(
		c.Request.Context(),
		capabilityID,
		userID.(string),
		req.Rating,
		req.Review,
		req.VersionUsed,
		req.Environment,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "rating submitted"})
}

// GetCapabilityRatings godoc
// @Summary Get ratings for a capability
// @Tags marketplace
// @Produce json
// @Param id path string true "Capability ID"
// @Param limit query int false "Result limit"
// @Param offset query int false "Result offset"
// @Success 200 {object} map[string]interface{}
// @Router /api/marketplace/capabilities/{id}/ratings [get]
func (h *MarketplaceHandler) GetCapabilityRatings(c *gin.Context) {
	capabilityID := c.Param("id")
	limit := 20
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	// TODO: Implement GetRatings method
	c.JSON(http.StatusOK, gin.H{
		"ratings": []interface{}{},
		"total":   0,
		"limit":   limit,
		"offset":  offset,
	})
}

// GetCapabilityStats godoc
// @Summary Get usage statistics for a capability
// @Tags marketplace
// @Produce json
// @Param id path string true "Capability ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/marketplace/capabilities/{id}/stats [get]
func (h *MarketplaceHandler) GetCapabilityStats(c *gin.Context) {
	capabilityID := c.Param("id")

	// TODO: Implement aggregated stats query
	c.JSON(http.StatusOK, gin.H{
		"capability_id":       capabilityID,
		"total_downloads":     0,
		"total_executions":    0,
		"success_rate":        0.0,
		"avg_execution_time":  0,
		"executions_by_env":   map[string]int{},
		"executions_last_30d": []map[string]interface{}{},
	})
}
