package domain

import (
	_ "embed"
	"strings"
)

//go:embed slot.go
var slotSource string

func ProvisionalMarkers() []string {
	var out []string
	for _, line := range strings.Split(slotSource, "\n") {
		if strings.Contains(line, "PROVISIONAL") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}
