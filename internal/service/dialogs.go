package service

import (
	"bytes"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
)

func rawDialogListInput(dialog *domain.Dialog, item int16) ([]byte, bool) {
	if dialog == nil || (dialog.Style != dialogStyleList && dialog.Style != dialogStyleTabList && dialog.Style != dialogStyleTabListHeaders) {
		return nil, false
	}
	rows := make([][]byte, 0)
	for _, row := range bytes.Split(dialog.RawMessage, []byte{'\n'}) {
		row = bytes.TrimSuffix(row, []byte{'\r'})
		if len(bytes.TrimSpace(row)) > 0 {
			rows = append(rows, row)
		}
	}
	if dialog.Style == dialogStyleTabListHeaders && len(rows) > 0 {
		rows = rows[1:]
	}
	if item < 0 || int(item) >= len(rows) {
		return []byte{}, true
	}
	selected := rows[item]
	if dialog.Style == dialogStyleTabList || dialog.Style == dialogStyleTabListHeaders {
		selected = bytes.SplitN(selected, []byte{'\t'}, 2)[0]
	}
	return stripDialogColorTags(selected), true
}

func stripDialogColorTags(input []byte) []byte {
	result := make([]byte, 0, len(input))
	for index := 0; index < len(input); {
		if input[index] == '{' {
			end := bytes.IndexByte(input[index:], '}')
			if end == 7 || end == 9 {
				valid := true
				for _, value := range input[index+1 : index+end] {
					if !((value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')) {
						valid = false
						break
					}
				}
				if valid {
					index += end + 1
					continue
				}
			}
		}
		result = append(result, input[index])
		index++
	}
	return result
}
