package samp

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func codecFor(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "gbk":
		return simplifiedchinese.GBK
	case "windows-1251", "windows1251", "cp1251":
		return charmap.Windows1251
	default:
		return nil
	}
}

func DecodeServerText(charset, value string) string {
	return decodeText(codecFor(charset), []byte(value))
}

func encodeText(codec encoding.Encoding, value string) ([]byte, error) {
	if codec == nil {
		return []byte(value), nil
	}
	// Android's String.getBytes(charset) replaces characters outside the
	// destination repertoire instead of failing the whole dialog response.
	return io.ReadAll(transform.NewReader(
		strings.NewReader(value),
		encoding.ReplaceUnsupported(codec.NewEncoder()),
	))
}

func decodeText(codec encoding.Encoding, value []byte) string {
	// The Android reference keeps already-UTF-8 text intact before trying the
	// configured legacy server charset. This also avoids decoding UTF-8 dialog
	// rows as GBK/Windows-1251 when a server sends UTF-8 text.
	if len(value) == 0 || utf8.Valid(value) {
		return string(value)
	}
	if codec == nil {
		return string(value)
	}
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(value), codec.NewDecoder()))
	if err != nil {
		return strings.ToValidUTF8(string(value), "�")
	}
	return string(out)
}
