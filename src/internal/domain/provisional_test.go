package domain

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProvisionalMarkers(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Log("could not determine caller for provisional scan")
		return
	}
	slotPath := filepath.Join(filepath.Dir(thisFile), "slot.go")

	f, err := os.Open(slotPath)
	if err != nil {
		t.Logf("could not open %s: %v", slotPath, err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if strings.Contains(scanner.Text(), "PROVISIONAL") {
			t.Logf("%s:%d: %s", slotPath, lineNo, strings.TrimSpace(scanner.Text()))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Logf("error scanning %s: %v", slotPath, err)
	}
}
