package httpapi

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>OpenComfy API</title>
  <!-- import UI: /import -->
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css"/>
  <style>
    html { color-scheme: light; }
    body { margin: 0; background: #fff; color: #3b4151; }
    .swagger-ui { background: #fff; }
    .swagger-ui .scheme-container { background: #fff; box-shadow: none; border-bottom: 1px solid #e8e8e8; }
    .topbar { display: none; }
  </style>
</head>
<body>
  <p style="margin:12px 16px;font:14px system-ui"><a href="/import">Import ComfyUI workflows</a></p>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      persistAuthorization: true,
      tryItOutEnabled: true,
      deepLinking: true,
      requestTimeout: 300000,
      defaultModelsExpandDepth: 1
    });
  </script>
</body>
</html>
`

func (s *Server) swaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(swaggerHTML))
}

func (s *Server) openapiYAMLHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(200)
	_, _ = w.Write(openapiYAML)
}

func (s *Server) openapiJSON(w http.ResponseWriter, r *http.Request) {
	var doc any
	if err := yaml.Unmarshal(openapiYAML, &doc); err != nil {
		writeError(w, 500, "api_error", "internal_error", err.Error(), "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}
