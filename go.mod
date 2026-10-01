module github.com/sourcednet/publisher

go 1.24

// Sibling projects, developed side by side in Dev/ until they are published.
replace github.com/sourcednet/core => ../core

replace github.com/sourcednet/testkit => ../testkit

replace github.com/sourcednet/resolver => ../resolver

require (
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.4.0
	github.com/sourcednet/core v0.0.0-00010101000000-000000000000
	github.com/sourcednet/resolver v0.0.0-00010101000000-000000000000
	github.com/sourcednet/testkit v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.43.0
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gowebpki/jcs v1.0.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	modernc.org/libc v1.66.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.38.2 // indirect
)
