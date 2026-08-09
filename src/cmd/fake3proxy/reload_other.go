//go:build !unix

package main

import "os"

func reloadSignal() <-chan os.Signal {
	return nil
}
