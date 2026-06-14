package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type HumaSetup struct {
	API    huma.API
	config huma.Config
}

type docAdapter struct {
	huma.Adapter
}

func (docAdapter) Handle(*huma.Operation, func(huma.Context)) {}

type docAPI struct {
	huma.API
	adapter docAdapter
}

func (d docAPI) Adapter() huma.Adapter {
	return d.adapter
}

func NewDocOnlyAPI(api huma.API) huma.API {
	return docAPI{API: api, adapter: docAdapter{api.Adapter()}}
}

func RegisterHumaDocs(engine *gin.Engine) HumaSetup {
	config := huma.DefaultConfig("AssetHub API", "1.0.0")
	config.Info.Description = "AssetHub file and asset management API for OpenAI-compatible files, media assets, metadata, tags, downloads, presigned URLs, and chunk uploads."
	config.Info.Contact = &huma.Contact{
		Name: "Saker",
		URL:  "https://github.com/saker-ai/assethub",
	}
	config.Info.License = &huma.License{
		Name: "Apache 2.0",
		URL:  "https://www.apache.org/licenses/LICENSE-2.0.html",
	}
	config.OpenAPI.Servers = []*huma.Server{
		{URL: "http://localhost:17040"},
	}
	if config.Components == nil {
		config.Components = &huma.Components{}
	}
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"BearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "AssetHub API key",
			Description:  "Bearer token for /v1 endpoints.",
		},
		"APIKeyAuth": {
			Type:        "apiKey",
			In:          "header",
			Name:        "X-API-Key",
			Description: "AssetHub API key header.",
		},
	}

	api := humagin.New(engine, config)
	doc := NewDocOnlyAPI(api)
	registerOpenAPIDocs(doc)
	engine.GET("/v1/openapi.json", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=300")
		c.JSON(http.StatusOK, api.OpenAPI())
	})
	engine.GET("/v1/openapi.yaml", func(c *gin.Context) {
		body, err := yaml.Marshal(api.OpenAPI())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "openapi_yaml_marshal"})
			return
		}
		c.Header("Cache-Control", "public, max-age=300")
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", body)
	})
	return HumaSetup{API: api, config: config}
}
