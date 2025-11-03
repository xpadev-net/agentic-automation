package version

// Commit はビルド時に ldflags で上書きされる Git コミットハッシュ
var Commit = "unknown"

// BuiltAt はビルド時刻（UTC ISO8601）。ldflags で上書きされる
var BuiltAt = "unknown"
