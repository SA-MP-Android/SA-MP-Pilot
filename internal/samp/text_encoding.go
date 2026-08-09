package samp

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func codecFor(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "gbk":
		return simplifiedchinese.GBK
	case "windows-1251":
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
	return io.ReadAll(transform.NewReader(strings.NewReader(value), codec.NewEncoder()))
}

func decodeText(codec encoding.Encoding, value []byte) string {
	if codec == nil {
		return string(value)
	}
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(value), codec.NewDecoder()))
	if err != nil {
		return strings.ToValidUTF8(string(value), "�")
	}
	return string(out)
}
