//go:build !linux

package main

import "os"

func reloadSignal() <-chan os.Signal {
	return nil
}
