// Package rules 内置构建时的上游拒绝句列表
package rules

import _ "embed"

// Rejected 是构建时的 rejected.txt 原文
//
//go:embed rejected.txt
var Rejected []byte
