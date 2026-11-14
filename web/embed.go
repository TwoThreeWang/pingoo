// Package webassets 把模板和静态文件打包进二进制。
package webassets

import "embed"

//go:embed templates static
var FS embed.FS
