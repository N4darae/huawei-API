//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func reloadSignal() <-chan os.Signal {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGUSR1)
	return ch
}
