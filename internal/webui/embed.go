// Package webui serves the interactive review interface without remote assets.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html app.js flow.js exploration.js dataflow-tools.js dataflow.js graph-tools.js graph-ui.js review-tools.js review-map.js logical-tools.js logical.js application-tools.js application.js walkthrough-tools.js walkthrough.js system-flow-tools.js system-flow.js endpoint-flow-tools.js endpoint-flow.js documentation-tools.js documentation.js markdown.js assessment-tools.js assessment.js assessment.css repository.js style.css exploration.css application.css visual-review-tools.js visual-review.js visual-review.css vendor/justgage.umd.js vendor/justgage.LICENSE vendor/marked.umd.js vendor/marked.LICENSE vendor/purify.min.js vendor/dompurify.LICENSE vendor/dompurify.LICENSE-MPL
//go:embed review-query-tools.js
//go:embed change-tools.js
//go:embed changes.js changes.css
//go:embed vendor/diff.min.js vendor/diff.LICENSE vendor/prism-core.min.js vendor/prism-clike.min.js vendor/prism-go.min.js vendor/prism-json.min.js vendor/prism-javascript.min.js vendor/prism-python.min.js vendor/prism-bash.min.js vendor/prism.LICENSE
//go:embed chat.js chat-tools.js chat.css
//go:embed workspace.js workspace.css
//go:embed tutorial.js tutorial.css
var assets embed.FS

func Handler() http.Handler {
	sub, _ := fs.Sub(assets, ".")
	return http.FileServer(http.FS(sub))
}
