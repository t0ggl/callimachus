package web

import (
	"embed"
	"io/fs"
)

// DistFS contains the embedded frontend static assets from web/dist.
//
//go:embed all:dist
var DistFS embed.FS

// FallbackHTML is served when web/dist/index.html is missing or empty.
const FallbackHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Callimachus</title>
</head>
<body>
    <div id="root">
        <h1>Callimachus</h1>
        <p>Loading application...</p>
    </div>
</body>
</html>`

// FS returns an fs.FS sub-tree rooted at "dist", or an error if "dist" cannot be opened.
func FS() (fs.FS, error) {
	return fs.Sub(DistFS, "dist")
}
