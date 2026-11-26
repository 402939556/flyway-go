package main

import _ "embed"

var (
	// Keep these as the raw bytes captured from Flyway OSS 13.13.0.
	// help-full.txt intentionally contains one lone LF alongside its CRLF lines.
	//go:embed official/help-short.txt
	officialHelpShort []byte

	//go:embed official/help-full.txt
	officialHelpFull []byte

	//go:embed official/banner.txt
	officialBanner []byte

	//go:embed official/version.txt
	officialVersion []byte

	//go:embed official/list-engines.txt
	officialListEngines []byte

	//go:embed official/list-engines-table.txt
	officialListEnginesTable []byte

	//go:embed official/version.json
	officialVersionJSON []byte

	//go:embed official/list-engines.json
	officialListEnginesJSON []byte
)
