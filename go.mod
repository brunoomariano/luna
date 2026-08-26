module github.com/brunoomariano/luna

go 1.27.0

require modernc.org/sqlite v1.56.0

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// The study clones third-party repositories under docs/luna_study/repos/, and
// several are Go modules. Without this, `go mod tidy` walks into them and pulls
// their dependencies into ours — the same trap that made lint-docs recurse into
// 1,697 vendored markdown files.
ignore docs/luna_study
