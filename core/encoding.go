package core

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// DecodeScript mirrors FileSystemResource.read(): use the configured charset,
// or detect the charset first when detectEncoding is enabled. UTF-8 BOM is
// tolerated by the parsers/checksum code, as in Flyway.
func DecodeScript(raw []byte, encoding string, detect bool) (string, error) {
	name := normalizeEncodingName(encoding)
	if detect {
		name = normalizeEncodingName(detectFileEncoding(raw))
	}
	if name == "" {
		name = "UTF-8"
	}
	name = strings.TrimSuffix(name, " BOM")
	switch name {
	case "UTF-8", "UTF8":
		if !utf8.Valid(raw) {
			return "", fmt.Errorf("Could not decode file using encoding UTF-8")
		}
		return string(raw), nil
	case "US-ASCII", "ASCII":
		for _, b := range raw {
			if b > 0x7F {
				return "", fmt.Errorf("Could not decode file using encoding US-ASCII")
			}
		}
		return string(raw), nil
	case "ISO-8859-1", "ISO8859-1", "LATIN1":
		runes := make([]rune, len(raw))
		for i, b := range raw {
			runes[i] = rune(b)
		}
		return string(runes), nil
	case "WINDOWS-1252", "CP1252":
		runes := make([]rune, len(raw))
		for i, b := range raw {
			runes[i] = decodeWindows1252(b)
		}
		return string(runes), nil
	case "UTF-16":
		return decodeUTF16(raw, true), nil
	case "UTF-16LE":
		return decodeUTF16(raw, false), nil
	case "UTF-16BE":
		return decodeUTF16(raw, true), nil
	default:
		return "", fmt.Errorf("Unsupported encoding: %s", encoding)
	}
}

// detectFileEncoding mirrors EncodingDetector.detectCharset.
func detectFileEncoding(buffer []byte) string {
	if charset := readCharsetHeader(buffer); charset != "" {
		return charset
	}
	if utf8.Valid(buffer) {
		return "UTF-8"
	}
	// ISO-8859-1 accepts every byte sequence, matching the Java loop.
	if isLikelyToBeUTF16(buffer) {
		return "UTF-16"
	}
	return "ISO-8859-1"
}

func readCharsetHeader(buffer []byte) string {
	if len(buffer) < 2 || len(buffer)%2 != 0 {
		return ""
	}
	if buffer[0] == 0xFE && buffer[1] == 0xFF {
		return "UTF-16BE"
	}
	if buffer[0] == 0xFF && buffer[1] == 0xFE {
		return "UTF-16LE"
	}
	return ""
}

func isLikelyToBeUTF16(buffer []byte) bool {
	if len(buffer)%2 != 0 {
		return false
	}
	matches := 0
	for i := 1; i < len(buffer); i += 2 {
		if buffer[i] == 0 {
			matches++
		} else {
			matches--
		}
	}
	return matches > 0
}

func decodeUTF16(raw []byte, defaultBigEndian bool) string {
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	bigEndian := defaultBigEndian
	if len(raw) >= 2 {
		if raw[0] == 0xFF && raw[1] == 0xFE {
			bigEndian = false
			raw = raw[2:]
		} else if raw[0] == 0xFE && raw[1] == 0xFF {
			bigEndian = true
			raw = raw[2:]
		}
	}
	u := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if bigEndian {
			u = append(u, uint16(raw[i])<<8|uint16(raw[i+1]))
		} else {
			u = append(u, uint16(raw[i+1])<<8|uint16(raw[i]))
		}
	}
	return string(utf16.Decode(u))
}

func decodeWindows1252(b byte) rune {
	if b < 0x80 || b >= 0xA0 {
		return rune(b)
	}
	table := [...]rune{
		0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
		0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
		0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
		0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
	}
	return table[b-0x80]
}

// IsSupportedEncoding is used by configuration validation helpers.
func IsSupportedEncoding(name string) bool {
	name = normalizeEncodingName(name)
	_, err := DecodeScript([]byte{}, name, false)
	return err == nil
}

var _ = bytes.Equal
