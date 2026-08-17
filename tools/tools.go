//go:build tools

// Package tools 记录 gomobile/gobind 工具依赖，供 CI 中 gomobile bind 使用。
package tools

import _ "golang.org/x/mobile/cmd/gobind"
